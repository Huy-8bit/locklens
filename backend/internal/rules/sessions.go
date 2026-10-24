package rules

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/locklens/locklens/internal/report"
)

func connectionRule(in Input, th Thresholds) []Finding {
	cur, ok := in.latest()
	if !ok {
		return []Finding{insufficient(RuleConnections, in.Now, "No recent activity collection is available for this target.")}
	}
	ratio := cur.UsageRatio()
	if ratio == nil {
		return []Finding{insufficient(RuleConnections, in.Now, "max_connections minus reserved slots is not positive; the usage ratio is undefined.")}
	}
	if *ratio < th.ConnectionsWarnRatio {
		return nil
	}
	n, first := in.streak(func(s ActivitySample) bool {
		r := s.UsageRatio()
		return r != nil && *r >= th.ConnectionsWarnRatio
	})
	if n < th.ConnectionsMinSamples {
		return nil
	}
	sev := Warning
	if *ratio >= th.ConnectionsCriticalRatio {
		sev = Critical
	}
	f := newFinding(RuleConnections, "_", sev, fmt.Sprintf("Connection usage at %.0f%% of available slots", *ratio*100), first, cur.At)
	f.Trigger = true
	c := in.View.Activity.Counts
	f.Observed = append(f.Observed, fmt.Sprintf("%d client connections out of %d available (max_connections %d minus %d reserved) for %d consecutive samples.",
		cur.UsedConnections, cur.MaxConnections-cur.Reserved, cur.MaxConnections, cur.Reserved, n))
	f.Evidence = append(f.Evidence,
		ev("used connections", cur.UsedConnections, "", "pg_stat_activity"),
		ev("max_connections", cur.MaxConnections, "", "pg_settings"),
		ev("reserved slots", cur.Reserved, "", "superuser_reserved_connections + reserved_connections"),
		ev("usage ratio", *ratio, "ratio", "derived"),
		ev("active", c.Active, "sessions", "pg_stat_activity.state"),
		ev("idle", c.Idle, "sessions", "pg_stat_activity.state"),
		ev("idle in transaction", c.IdleInTx+c.IdleInTxAborted, "sessions", "pg_stat_activity.state"),
		ev("state unknown", c.UnknownState, "sessions", "pg_stat_activity.state"))
	f.Explanation = "Few connection slots are left; new connections from regular roles will fail once they are exhausted."
	f.PossibleExplanations = []string{
		"Connection pools are sized larger than the server can accept.",
		"Sessions pile up because they wait for locks or slow queries.",
		"Idle connections are not released by clients.",
	}
	f.NextChecks = []string{"Compare active versus idle sessions above.", "Check for blocking and long transactions.", "Review pool sizes of the applications connecting to this server."}
	f.Limitations = "Counts include LockLens' own connections. Connections are sampled; short spikes between samples are not seen."
	return []Finding{f}
}

func longTransactionRule(in Input, th Thresholds) []Finding {
	if _, ok := in.latest(); !ok {
		return []Finding{insufficient(RuleLongTransaction, in.Now, "No recent activity collection is available for this target.")}
	}
	rep := in.View.Report
	type cand struct {
		pid int
		age float64
	}
	var cands []cand
	for _, a := range rep.Analysis.Sessions {
		if a.TransactionAgeSeconds != nil && *a.TransactionAgeSeconds >= th.LongTxWarnSeconds {
			cands = append(cands, cand{a.PID, *a.TransactionAgeSeconds})
		}
	}
	slices.SortFunc(cands, func(a, b cand) int { return cmp.Compare(b.age, a.age) })
	if len(cands) > 5 {
		cands = cands[:5]
	}
	nodes := map[int]report.Node{}
	for _, n := range rep.Analysis.Nodes {
		nodes[n.ID] = n
	}
	var out []Finding
	for _, c := range cands {
		s, ok := rep.Snapshot.SessionByPID(c.pid)
		if !ok || s.BackendType == nil || *s.BackendType != "client backend" || s.XactStart == nil {
			continue
		}
		sev := Warning
		if c.age >= th.LongTxCriticalSeconds {
			sev = Critical
		}
		key := fmt.Sprintf("pid:%d@%d", c.pid, s.XactStart.Unix())
		f := newFinding(RuleLongTransaction, key, sev, fmt.Sprintf("Transaction of pid %d open for %s", c.pid, fmtDuration(c.age)), *s.XactStart, rep.Snapshot.ServerTime)
		f.Observed = append(f.Observed, fmt.Sprintf("xact_start is %s; the transaction has been open for %s (state: %s).",
			s.XactStart.UTC().Format("2006-01-02 15:04:05 MST"), fmtDuration(c.age), strOr(s.State, "unknown")))
		f.Evidence = append(f.Evidence,
			ev("pid", c.pid, "", "pg_stat_activity"),
			ev("transaction age", c.age, "s", "pg_stat_activity.xact_start"),
			ev("state", strOr(s.State, "unknown"), "", "pg_stat_activity.state"),
			ev("user", strOr(s.User, "unknown"), "", "pg_stat_activity.usename"),
			ev("application", strOr(s.ApplicationName, "unknown"), "", "pg_stat_activity.application_name"),
			ev("blocks directly", nodes[c.pid].DirectBlocked, "sessions", "pg_blocking_pids"))
		f.Explanation = "A long-open transaction holds its locks and its snapshot; it can block other sessions and keeps VACUUM from removing dead rows newer than its snapshot."
		f.PossibleExplanations = []string{"A batch job or migration running as one transaction.", "An application leaving a transaction open (see the state).", "A forgotten interactive session."}
		f.NextChecks = []string{"Check whether this transaction blocks anyone (blocking explorer).", "Check dead tuples on busy tables (table health).", "Identify the client and whether the duration is expected."}
		f.Limitations = "Only transactions visible to the monitoring role are evaluated; with restricted visibility, other roles' transactions are invisible unless they block others."
		out = append(out, f)
	}
	return out
}
