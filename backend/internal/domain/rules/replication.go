package rules

import (
	"fmt"
)

func replicationRules(in Input, th Thresholds) []Finding {
	r := in.View.Replication
	if r == nil {
		why := "Replication state has not been collected yet."
		return []Finding{insufficient(RuleReplicaLag, in.Now, why), insufficient(RuleSlotRetainedWAL, in.Now, why)}
	}
	var out []Finding
	for _, x := range r.Replicas {
		label := x.ApplicationName
		if label == "" {
			label = fmt.Sprintf("pid %d", x.PID)
		}
		h := in.View.Points["replication.replay_lag_bytes{"+label+"}"]
		n := 0
		for i := len(h) - 1; i >= 0; i-- {
			if h[i].Value == nil || *h[i].Value < th.ReplicaLagBytes {
				break
			}
			n++
		}
		if n < th.ReplicaLagMinSamples || x.ReplayLagBytes == nil {
			continue
		}
		f := newFinding(RuleReplicaLag, "replica:"+label, Warning,
			fmt.Sprintf("Replica %s replay is %s behind", label, fmtBytes(*x.ReplayLagBytes)), h[len(h)-n].At, r.At)
		f.Observed = append(f.Observed, fmt.Sprintf("The replica's replay position is %s behind the primary's current WAL position for %d consecutive samples.", fmtBytes(*x.ReplayLagBytes), n))
		f.Evidence = append(f.Evidence, ev("replica", label, "", "pg_stat_replication.application_name"),
			ev("replay lag", *x.ReplayLagBytes, "bytes", "pg_wal_lsn_diff(pg_current_wal_lsn(), replay_lsn)"),
			ev("state", strOr(x.State, "unknown"), "", "pg_stat_replication.state"))
		if x.ReplayLagSec != nil {
			f.Evidence = append(f.Evidence, ev("replay_lag", *x.ReplayLagSec, "s", "pg_stat_replication.replay_lag"))
		}
		f.Explanation = "WAL generated on the primary has not yet been replayed on this replica."
		f.PossibleExplanations = []string{"Replay is paused or blocked by a conflicting query on the standby.", "The replica's I/O or CPU cannot keep up.", "Network throughput limits."}
		f.NextChecks = []string{"Compare sent, write, flush, and replay positions to locate the bottleneck.", "Check the standby for replay pause and long queries."}
		f.Limitations = "Byte lag is the WAL distance, not time. replay_lag (time) is NULL when the replica is idle and caught up, and is measured from standby feedback."
		out = append(out, f)
	}
	if s := r.Standby; s != nil && s.ReplayDelayBytes != nil && *s.ReplayDelayBytes >= th.ReplicaLagBytes {
		f := newFinding(RuleReplicaLag, "standby", Warning, fmt.Sprintf("This standby has %s of received WAL not yet replayed", fmtBytes(*s.ReplayDelayBytes)), r.At, r.At)
		f.Observed = append(f.Observed, fmt.Sprintf("Received WAL is ahead of replayed WAL by %s (pause state: %s).", fmtBytes(*s.ReplayDelayBytes), strOr(s.ReplayPauseState, "unknown")))
		f.Evidence = append(f.Evidence, ev("replay delay", *s.ReplayDelayBytes, "bytes", "pg_wal_lsn_diff(receive, replay)"),
			ev("replay pause state", strOr(s.ReplayPauseState, "unknown"), "", "pg_get_wal_replay_pause_state()"))
		f.Explanation = "The standby receives WAL faster than it replays it."
		f.PossibleExplanations = []string{"Replay is paused (pg_wal_replay_pause).", "Queries on the standby conflict with replay (max_standby_streaming_delay)."}
		f.NextChecks = []string{"Check the replay pause state and conflicting queries on the standby."}
		f.Limitations = "This measures received-but-not-replayed WAL only; WAL not yet received from the primary is not visible from the standby. Time since the last replayed transaction is not used as lag because it grows whenever the primary is idle."
		out = append(out, f)
	}
	for _, s := range r.Slots {
		if s.RetainedBytes == nil || *s.RetainedBytes < th.SlotRetainedBytes {
			continue
		}
		sev := Warning
		if s.WALStatus != nil && (*s.WALStatus == "unreserved" || *s.WALStatus == "lost") {
			sev = Critical
		}
		f := newFinding(RuleSlotRetainedWAL, "slot:"+s.Name, sev, fmt.Sprintf("Replication slot %s retains %s of WAL", s.Name, fmtBytes(*s.RetainedBytes)), r.At, r.At)
		f.Trigger = true
		f.Observed = append(f.Observed, fmt.Sprintf("restart_lsn of slot %s is %s behind the current WAL position (active: %t, wal_status: %s).",
			s.Name, fmtBytes(*s.RetainedBytes), s.Active, strOr(s.WALStatus, "unknown")))
		f.Evidence = append(f.Evidence, ev("slot", s.Name, "", "pg_replication_slots"), ev("type", s.Type, "", "pg_replication_slots"),
			ev("active", s.Active, "", "pg_replication_slots"), ev("retained WAL", *s.RetainedBytes, "bytes", "pg_wal_lsn_diff(current, restart_lsn)"),
			ev("wal_status", strOr(s.WALStatus, "unknown"), "", "pg_replication_slots"))
		if s.SafeWALSize != nil {
			f.Evidence = append(f.Evidence, ev("safe_wal_size", *s.SafeWALSize, "bytes", "pg_replication_slots"))
		}
		f.Explanation = "PostgreSQL keeps WAL from restart_lsn onward for this slot; if the consumer does not advance, pg_wal grows."
		f.PossibleExplanations = []string{"The consumer (standby or logical subscriber) is disconnected or slow.", "An abandoned slot is no longer used."}
		if !s.Active {
			f.PossibleExplanations = append([]string{"The slot is inactive: no consumer is connected."}, f.PossibleExplanations...)
		}
		f.NextChecks = []string{"Identify the slot's consumer and whether it is still needed.", "Check free space for pg_wal (host metrics, if configured)."}
		f.Limitations = "Retained bytes are an upper bound computed from LSN distance; actual files kept also depend on wal_keep_size and checkpoints. LockLens never drops slots."
		out = append(out, f)
	}
	return out
}
