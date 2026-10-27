package rules

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/locklens/locklens/internal/domain/baseline"
	"github.com/locklens/locklens/internal/domain/finding"
	"github.com/locklens/locklens/internal/domain/report"
)

// longRunningRule reports statements that have been executing for long, from
// the latest activity snapshot, while they are still running: either beyond
// the absolute thresholds, or far beyond what the statement usually takes
// (its baseline mean time per call).
func longRunningRule(in Input, th Thresholds) []Finding {
	if _, ok := in.latest(); !ok {
		return []Finding{insufficient(RuleLongRunning, in.Now, "No recent activity collection is available for this target.")}
	}
	rep := in.View.Report
	expected := expectedByQueryID(in, th)
	nodes := map[int]report.Node{}
	for _, n := range rep.Analysis.Nodes {
		nodes[n.ID] = n
	}
	type cand struct {
		pid      int
		age      float64
		sev      Severity
		expected float64 // ms, 0 when unknown
		reason   string
	}
	var cands []cand
	for _, a := range rep.Analysis.Sessions {
		if a.QueryAgeSeconds == nil {
			continue
		}
		s, ok := rep.Snapshot.SessionByPID(a.PID)
		if !ok || s.LeaderPID != nil || s.BackendType == nil || *s.BackendType != "client backend" ||
			s.State == nil || *s.State != "active" || s.QueryStart == nil {
			continue
		}
		age := *a.QueryAgeSeconds
		c := cand{pid: a.PID, age: age}
		switch {
		case age >= th.SlowQueryCriticalSeconds:
			c.sev, c.reason = Critical, fmt.Sprintf("beyond the critical threshold of %s", fmtDuration(th.SlowQueryCriticalSeconds))
		case age >= th.SlowQueryWarnSeconds:
			c.sev, c.reason = Warning, fmt.Sprintf("beyond the warning threshold of %s", fmtDuration(th.SlowQueryWarnSeconds))
		}
		if s.QueryID != nil {
			if exp, ok := expected[*s.QueryID]; ok {
				c.expected = exp
				limit := math.Max(th.SlowQueryExpectedMinSeconds, th.SlowQueryExpectedFactor*exp/1000)
				if age >= limit && c.sev == "" {
					c.sev = Warning
					c.reason = fmt.Sprintf("%.0fx its usual time per call (%s)", age*1000/exp, fmtMS(exp))
				}
			}
		}
		if c.sev != "" {
			cands = append(cands, c)
		}
	}
	slices.SortFunc(cands, func(a, b cand) int { return cmp.Compare(b.age, a.age) })
	if len(cands) > maxPerRule {
		cands = cands[:maxPerRule]
	}
	var out []Finding
	for _, c := range cands {
		s, _ := rep.Snapshot.SessionByPID(c.pid)
		// One finding per execution: the pid with its backend start (pids are
		// reused) and the query start.
		var backendStart int64
		if s.BackendStart != nil {
			backendStart = s.BackendStart.Unix()
		}
		key := fmt.Sprintf("pid:%d:%d:%d", c.pid, backendStart, s.QueryStart.UnixNano())
		title := fmt.Sprintf("Query running for %s in pid %d", fmtDuration(c.age), c.pid)
		if s.QueryID != nil {
			title = fmt.Sprintf("Query %d running for %s in pid %d", *s.QueryID, fmtDuration(c.age), c.pid)
		}
		f := newFinding(RuleLongRunning, key, c.sev, title, *s.QueryStart, rep.Snapshot.ServerTime)
		f.Trigger = c.sev == Critical
		f.Observed = append(f.Observed, fmt.Sprintf("The current statement started at %s and has been executing for %s, %s.",
			s.QueryStart.UTC().Format("2006-01-02 15:04:05 MST"), fmtDuration(c.age), c.reason))
		wait := "none (on CPU or not reported)"
		if s.WaitEventType != nil {
			wait = *s.WaitEventType + ": " + strOr(s.WaitEvent, "?")
		}
		f.Evidence = append(f.Evidence,
			ev("pid", c.pid, "", "pg_stat_activity"),
			ev("running for", c.age, "s", "pg_stat_activity.query_start"),
			ev("database", strOr(s.Database, "unknown"), "", "pg_stat_activity.datname"),
			ev("user", strOr(s.User, "unknown"), "", "pg_stat_activity.usename"),
			ev("application", strOr(s.ApplicationName, "unknown"), "", "pg_stat_activity.application_name"),
			ev("wait event", wait, "", "pg_stat_activity.wait_event"))
		if s.QueryID != nil {
			f.Evidence = append(f.Evidence, ev("queryid", fmt.Sprint(*s.QueryID), "", "pg_stat_activity.query_id"))
		}
		if c.expected > 0 {
			f.Evidence = append(f.Evidence, ev("usual time per call", c.expected, "ms", "baseline median of pg_stat_statements means"))
		}
		if s.ParallelWorkers > 0 {
			f.Evidence = append(f.Evidence, ev("parallel workers", s.ParallelWorkers, "", "pg_stat_activity.leader_pid"))
		}
		n := nodes[c.pid]
		if len(n.BlockedBy) > 0 {
			f.Evidence = append(f.Evidence, ev("blocked by", joinInts(n.BlockedBy), "pids", "pg_blocking_pids"))
		}
		if n.TransitiveBlocked > 0 {
			f.Evidence = append(f.Evidence, ev("sessions waiting behind it", n.TransitiveBlocked, "sessions", "pg_blocking_pids"))
		}
		metrics := map[string]float64{"running_seconds": c.age, "blocked_sessions": float64(n.TransitiveBlocked)}
		if c.expected > 0 {
			metrics["expected_ms"] = c.expected
			metrics["ratio"] = c.age * 1000 / c.expected
		}
		f.Metrics = metrics
		f.Confidence = &finding.Confidence{Level: finding.ConfidenceHigh, Reasons: []string{"the running time is read directly from pg_stat_activity.query_start"}}
		f.Explanation = "A statement is still running after a long time. It holds its locks and a snapshot for as long as it runs, and its cost is not in pg_stat_statements until it finishes."
		switch {
		case len(n.BlockedBy) > 0:
			f.PossibleExplanations = []string{"It is waiting for a lock held by another session (see blocked by): the blocker, not this statement, is the first thing to check."}
		case s.WaitEventType != nil && *s.WaitEventType == "IO":
			f.PossibleExplanations = []string{"It reads a lot of data from disk: a large scan or a plan that does not use an index.", "The storage is slow or saturated."}
		default:
			f.PossibleExplanations = []string{"A heavy statement (report, batch, migration) for which this duration may be expected.",
				"A plan that processes far more rows than intended.", "Waiting on a lock, I/O, or the client (see the wait event)."}
		}
		f.NextChecks = []string{"Open the session in the blocking explorer to see what it waits for and whom it blocks.",
			"If the statement is known, compare it with its baseline on its statement page.",
			"Decide with its owner whether to let it finish; LockLens never cancels queries."}
		f.Limitations = "Based on activity snapshots taken at each collection, so the duration is accurate to the collection interval. query_id requires compute_query_id (PostgreSQL 14+); without it the usual duration is unknown and only the absolute thresholds apply. Statements of other roles are visible only with pg_read_all_stats."
		out = append(out, f)
	}
	return out
}

// expectedByQueryID indexes the usual mean time per call of warm baselines by
// queryid. pg_stat_activity does not expose the user or database ids that
// complete a pg_stat_statements identity, so when one queryid has several
// identities the largest usual time is taken: the rule then reports less.
func expectedByQueryID(in Input, th Thresholds) map[int64]float64 {
	out := map[int64]float64{}
	for k, b := range in.Baselines {
		prof, _ := b.For(in.Now)
		st, ok := prof.Stats[baseline.MeanMS]
		if b.All.Points < th.BaselineWarmupPoints || st.N < baseline.MinProfilePoints || !ok || st.Median <= 0 {
			continue
		}
		if st.Median > out[k.QueryID] {
			out[k.QueryID] = st.Median
		}
	}
	return out
}

func fmtMS(ms float64) string {
	if ms >= 1000 {
		return fmtDuration(ms / 1000)
	}
	return fmt.Sprintf("%.1f ms", ms)
}

func joinInts(v []int) string {
	s := make([]string, len(v))
	for i, x := range v {
		s[i] = fmt.Sprint(x)
	}
	return strings.Join(s, ", ")
}
