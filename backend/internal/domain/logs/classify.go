package logs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/locklens/locklens/internal/lib/pglog"
	"github.com/locklens/locklens/internal/lib/sqlparse"
)

var (
	reDuration  = regexp.MustCompile(`(?s)^duration: ([0-9.]+) ms(?:\s+(statement|execute [^:]*|bind [^:]*|parse [^:]*|plan):\s*(.*))?$`)
	reLockWait  = regexp.MustCompile(`^process (\d+) (still waiting for|acquired) (\S+) on (.+?) after ([0-9.]+) ms`)
	reHolders   = regexp.MustCompile(`Process(?:es)? holding the lock: ([0-9, ]+)\.(?: Wait queue: ([0-9, ]+)\.)?`)
	reDLWait    = regexp.MustCompile(`Process (\d+) waits for (\S+) on (.+?); blocked by process (\d+)\.`)
	reDLStmt    = regexp.MustCompile(`(?m)^Process (\d+): (.*)$`)
	reVacuum    = regexp.MustCompile(`^automatic (aggressive )?vacuum (?:to prevent wraparound )?of table "([^"]+)"`)
	reAnalyze   = regexp.MustCompile(`^automatic analyze of table "([^"]+)"`)
	reElapsed   = regexp.MustCompile(`elapsed: ([0-9.]+) s`)
	reTempFile  = regexp.MustCompile(`^temporary file: path "[^"]*", size (\d+)`)
	reCkpt      = regexp.MustCompile(`^checkpoint complete: wrote (\d+) buffers.*?total=([0-9.]+) s`)
	reQueryText = regexp.MustCompile(`(?s)^Query Text: (.*?)\n(\s*\S.*)$`)
	rePlanNode  = regexp.MustCompile(`(?m)^\s*(?:->\s+)?[A-Z][A-Za-z ]+.*\((?:cost|actual)=`)
)

// Classify turns a log record into an event; false for records LockLens
// does not keep (connections, notices, and other messages).
func Classify(r pglog.Record) (Event, bool) {
	e := Event{At: r.Time, PID: r.PID, DatName: r.Database, UserName: r.User, Application: r.Application,
		Severity: r.Severity, SQLState: r.SQLState, Details: map[string]any{}}
	if r.QueryID != 0 {
		q := r.QueryID
		e.QueryID = &q
	}
	msg := r.Message
	switch {
	case reDuration.MatchString(msg):
		m := reDuration.FindStringSubmatch(msg)
		d, _ := strconv.ParseFloat(m[1], 64)
		e.DurationMS = &d
		if m[2] == "plan" {
			e.Kind = KindPlan
			query, plan := splitPlan(m[3])
			setQuery(&e, query)
			e.Details["plan"] = plan
			e.Message = "auto_explain plan"
		} else {
			e.Kind = KindSlowQuery
			stmt := m[3]
			if stmt == "" {
				stmt = r.Statement // log_duration without the statement
			}
			setQuery(&e, stmt)
			if m[2] != "" && m[2] != "statement" {
				e.Details["phase"] = strings.Fields(m[2])[0] // execute, bind, parse
			}
			e.Message = "duration " + m[1] + " ms"
		}
	case reLockWait.MatchString(msg):
		m := reLockWait.FindStringSubmatch(msg)
		e.Kind = KindLockWait
		d, _ := strconv.ParseFloat(m[5], 64)
		e.DurationMS = &d
		e.Details["mode"], e.Details["object"], e.Details["acquired"] = m[3], m[4], m[2] == "acquired"
		if h := reHolders.FindStringSubmatch(r.Detail); h != nil {
			e.Details["holders"], e.Details["waiters"] = pids(h[1]), pids(h[2])
		}
		e.Message = "process " + m[1] + " " + m[2] + " " + m[3] + " on " + m[4]
		setQuery(&e, r.Statement)
	case r.SQLState == "40P01" || msg == "deadlock detected":
		e.Kind = KindDeadlock
		e.Message = "deadlock detected"
		var edges []map[string]any
		for _, m := range reDLWait.FindAllStringSubmatch(r.Detail, -1) {
			edges = append(edges, map[string]any{"pid": atoi(m[1]), "mode": m[2], "object": m[3], "blocked_by": atoi(m[4])})
		}
		stmts := map[string]string{}
		for _, m := range reDLStmt.FindAllStringSubmatch(r.Detail, -1) {
			n, _ := sqlparse.Normalize(m[2])
			stmts[m[1]] = n
		}
		e.Details["cycle"], e.Details["statements"] = edges, stmts
		setQuery(&e, r.Statement)
	case reVacuum.MatchString(msg):
		m := reVacuum.FindStringSubmatch(msg)
		e.Kind = KindAutovacuum
		e.Details["table"], e.Details["aggressive"] = m[2], m[1] != ""
		e.Details["report"] = msg
		setElapsed(&e, msg)
		e.Message = "automatic vacuum of " + m[2]
	case reAnalyze.MatchString(msg):
		m := reAnalyze.FindStringSubmatch(msg)
		e.Kind = KindAutoanalyze
		e.Details["table"], e.Details["report"] = m[1], msg
		setElapsed(&e, msg)
		e.Message = "automatic analyze of " + m[1]
	case reTempFile.MatchString(msg):
		m := reTempFile.FindStringSubmatch(msg)
		e.Kind = KindTempFile
		b, _ := strconv.ParseInt(m[1], 10, 64)
		e.Details["bytes"] = b
		e.Message = "temporary file of " + m[1] + " bytes"
		setQuery(&e, r.Statement)
	case reCkpt.MatchString(msg):
		m := reCkpt.FindStringSubmatch(msg)
		e.Kind = KindCheckpoint
		total, _ := strconv.ParseFloat(m[2], 64)
		d := total * 1000
		e.DurationMS = &d
		e.Details["buffers"], e.Details["report"] = atoi(m[1]), msg
		e.Message = "checkpoint complete"
	case r.Severity == "ERROR" || r.Severity == "FATAL" || r.Severity == "PANIC":
		e.Kind = KindError
		e.Message = Redact(msg)
		setQuery(&e, r.Statement)
	default:
		return Event{}, false
	}
	e.ID = eventID(r)
	return e, true
}

// setQuery stores a statement normalised, never with its constants.
func setQuery(e *Event, sql string) {
	sql = strings.TrimSpace(sql)
	if sql == "" {
		return
	}
	e.Query, e.Fingerprint = sqlparse.Normalize(sql)
}

func setElapsed(e *Event, msg string) {
	if m := reElapsed.FindStringSubmatch(msg); m != nil {
		s, _ := strconv.ParseFloat(m[1], 64)
		d := s * 1000
		e.DurationMS = &d
	}
}

// splitPlan separates auto_explain output into the statement and the plan
// (JSON or text), with constants removed from both.
func splitPlan(s string) (query string, plan any) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{") {
		var j map[string]any
		if json.Unmarshal([]byte(s), &j) == nil {
			q, _ := j["Query Text"].(string)
			delete(j, "Query Text")
			redactPlanJSON(j)
			return q, j
		}
	}
	if m := reQueryText.FindStringSubmatch(s); m != nil {
		// The statement may span lines: it ends where the first plan node
		// starts.
		rest := m[1] + "\n" + m[2]
		if loc := rePlanNode.FindStringIndex(rest); loc != nil {
			return strings.TrimSpace(rest[:loc[0]]), RedactPlanText(rest[loc[0]:])
		}
		return m[1], RedactPlanText(m[2])
	}
	return "", RedactPlanText(s)
}

func pids(s string) []int {
	var out []int
	for _, p := range strings.Split(s, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// eventID identifies a log message: the same message read twice (a batch
// sent again) has the same ID.
func eventID(r pglog.Record) string {
	h := sha256.New()
	h.Write([]byte(r.Time.Format("2006-01-02T15:04:05.000000Z07:00")))
	h.Write([]byte{0})
	h.Write([]byte(strconv.Itoa(r.PID)))
	h.Write([]byte{0})
	h.Write([]byte(strconv.FormatInt(r.SessionLine, 10)))
	h.Write([]byte{0})
	h.Write([]byte(r.Message))
	return hex.EncodeToString(h.Sum(nil)[:12])
}
