package rules

import (
	"fmt"
	"time"
)

// Reachability is how a target's connection and activity collection have
// fared since the last success.
type Reachability struct {
	// FailingSince is the first failure after the last success.
	FailingSince time.Time
	// Attempts are the failed attempts since then.
	Attempts int
	// Connected is false when the failures are connection failures; true
	// when connecting works but collecting activity fails.
	Connected bool
	// Error is the latest error, redacted.
	Error string
	// LastSuccess is the last successful activity collection, if any.
	LastSuccess *time.Time
}

// Unreachable reports target.unreachable once failures have lasted
// TargetDownSeconds (and at least two attempts failed); nothing before.
func Unreachable(r Reachability, now time.Time, th Thresholds) []Finding {
	down := now.Sub(r.FailingSince).Seconds()
	if r.FailingSince.IsZero() || r.Attempts < 2 || down < th.TargetDownSeconds {
		return nil
	}
	// The title stays true while the incident lasts; how long is in the
	// observation and the incident's duration.
	title := "Instance cannot be reached"
	if r.Connected {
		title = "Instance accepts connections but its activity cannot be collected"
	}
	f := newFinding(RuleTargetUnreachable, "target", Critical, title, r.FailingSince, now)
	f.Observed = append(f.Observed, fmt.Sprintf("No activity collected for %s: %d attempt(s) failed since %s; the latest error: %s",
		fmtDuration(down), r.Attempts, r.FailingSince.UTC().Format(time.RFC3339), r.Error))
	last := "never since LockLens started"
	if r.LastSuccess != nil {
		last = r.LastSuccess.UTC().Format(time.RFC3339)
	}
	f.Evidence = append(f.Evidence,
		ev("failing for", down, "seconds", "LockLens collector"),
		ev("failed attempts", r.Attempts, "", "LockLens collector"),
		ev("last successful collection", last, "", "LockLens collector"),
		ev("latest error", r.Error, "", "LockLens collector"))
	f.Metrics = map[string]float64{"down_seconds": down, "attempts": float64(r.Attempts)}
	f.Explanation = "LockLens has not collected this instance's activity for longer than target_down_seconds. Nothing about the instance is known meanwhile, including blocking or load."
	f.PossibleExplanations = []string{"The server is down or restarting (crash, failover, maintenance).",
		"The network between LockLens and the server is broken, or a firewall changed.",
		"The monitoring role's password, permissions, or pg_hba.conf entry changed.",
		"The server refuses connections: max_connections reached, or it is shutting down."}
	if r.Connected {
		f.PossibleExplanations = []string{"Activity queries time out: the server is overloaded or blocked on its catalog.",
			"The monitoring role lost a privilege it needs (pg_read_all_stats)."}
	}
	f.NextChecks = []string{"Check the server's process and its log, and whether a failover happened (Changes).",
		"Connect with the monitoring role from the LockLens host (Settings → Instances → Test).",
		"Check max_connections and the connections of other clients."}
	f.Limitations = "LockLens sees only its own attempts: an unreachable instance may still serve clients on another network path."
	return []Finding{f}
}
