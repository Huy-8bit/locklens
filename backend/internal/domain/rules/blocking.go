package rules

import (
	"fmt"
	"slices"

	"github.com/locklens/locklens/internal/domain/model"
	"github.com/locklens/locklens/internal/domain/report"
)

const visibilityLimitation = " Some sessions' details are hidden from the monitoring role (no pg_read_all_stats), so states, ages, and queries may be unknown."

func hasRestrictedVisibility(rep *report.Report) bool {
	if rep == nil {
		return false
	}
	for _, w := range rep.Snapshot.Warnings {
		if w.Code == model.WarnRestrictedVisibility {
			return true
		}
	}
	return false
}

// blockingRules evaluates blocking.long_chain and blocking.idle_in_tx_blocker.
func blockingRules(in Input, th Thresholds) []Finding {
	cur, ok := in.latest()
	if !ok {
		why := "No recent activity collection is available for this target."
		return []Finding{insufficient(RuleBlockingChain, in.Now, why), insufficient(RuleIdleBlocker, in.Now, why)}
	}
	restricted := hasRestrictedVisibility(in.View.Report)
	var out []Finding
	for _, root := range cur.Roots {
		key := root.Key
		present := func(s ActivitySample) bool {
			return slices.ContainsFunc(s.Roots, func(r RootSample) bool { return r.Key == key })
		}
		n, first := in.streak(present)
		dur := cur.At.Sub(first).Seconds()

		if root.Transitive >= th.BlockingMinBlocked && n >= th.BlockingMinSamples && dur >= th.BlockingMinSeconds {
			sev := Warning
			if root.Transitive >= th.BlockingCriticalBlocked || dur >= th.BlockingCriticalSeconds {
				sev = Critical
			}
			title := fmt.Sprintf("Blocking chain rooted at pid %d persists (%d blocked)", root.PID, root.Transitive)
			if root.Kind == report.KindPreparedTransaction {
				title = fmt.Sprintf("Prepared transaction (PID 0) blocks %d session(s)", root.Transitive)
			}
			if root.Cyclic {
				title = fmt.Sprintf("Cyclic blocking among pids %v persists", root.Members)
			}
			f := newFinding(RuleBlockingChain, key, sev, title, first, cur.At)
			f.Trigger = true
			f.Observed = append(f.Observed,
				fmt.Sprintf("pg_blocking_pids() reported this blocking in %d consecutive samples over %s.", n, fmtDuration(dur)),
				fmt.Sprintf("The root blocks %d session(s) directly and %d in total.", root.Direct, root.Transitive))
			f.Evidence = append(f.Evidence,
				ev("root pid", root.PID, "", "pg_blocking_pids"),
				ev("root kind", root.Kind, "", "locklens graph"),
				ev("root state", strOr(root.State, "unknown"), "", "pg_stat_activity"),
				ev("directly blocked", root.Direct, "sessions", "pg_blocking_pids"),
				ev("transitively blocked (unique)", root.Transitive, "sessions", "pg_blocking_pids"),
				ev("consecutive samples", n, "", "locklens history"),
				ev("observed duration", dur, "s", "locklens history"),
				ev("component members", root.Members, "pids", "pg_blocking_pids"))
			if root.MaxLockWait != nil {
				f.Evidence = append(f.Evidence, ev("longest lock wait in chain", *root.MaxLockWait, "s", "pg_locks.waitstart"))
			}
			if root.TxAge != nil {
				f.Evidence = append(f.Evidence, ev("root transaction age", *root.TxAge, "s", "pg_stat_activity.xact_start"))
			}
			f.Explanation = "Sessions are waiting for locks held (or requested earlier) by the root session, and the situation has lasted longer than the configured threshold."
			f.PossibleExplanations = []string{
				"The root transaction is long-running or waiting on the application (for example idle in transaction).",
				"A DDL statement (ALTER TABLE, CREATE INDEX without CONCURRENTLY) holds or waits for a strong lock and makes other sessions queue behind it.",
				"Hot rows are updated concurrently by many sessions.",
			}
			if root.Cyclic {
				f.PossibleExplanations = []string{"A transient state captured by a non-atomic snapshot.", "A deadlock that PostgreSQL has not resolved yet (it does so after deadlock_timeout)."}
			}
			f.NextChecks = []string{
				"Open the blocking explorer to see the full tree, lock modes, and waiting relations.",
				"Check what the root session's application is doing and whether its transaction should be open.",
				"Review the root's transaction age and last query (enable query text for this target if policy allows).",
			}
			f.Limitations = "Blocking is sampled at the collection interval; brief waits between samples are not seen. A chain is not a deadlock: it resolves when the root commits or rolls back. The snapshot is assembled from several catalog reads and is not atomic."
			if root.Cyclic {
				f.Limitations += " A cycle in sampled data is not a confirmed deadlock."
			}
			if restricted {
				f.Limitations += visibilityLimitation
			}
			out = append(out, f)
		}

		if root.State != nil && (*root.State == "idle in transaction" || *root.State == "idle in transaction (aborted)") {
			idleKey := key
			idle := func(s ActivitySample) bool {
				return slices.ContainsFunc(s.Roots, func(r RootSample) bool {
					return r.Key == idleKey && r.State != nil && (*r.State == "idle in transaction" || *r.State == "idle in transaction (aborted)")
				})
			}
			m, firstIdle := in.streak(idle)
			if m >= th.IdleBlockerMinSamples {
				f := newFinding(RuleIdleBlocker, key, Critical, fmt.Sprintf("Idle-in-transaction session pid %d blocks %d session(s)", root.PID, root.Transitive), firstIdle, cur.At)
				f.Trigger = true
				f.Observed = append(f.Observed,
					fmt.Sprintf("pid %d was idle in transaction and a root blocker in %d consecutive samples.", root.PID, m),
					"While idle in transaction it runs no statement, so it cannot finish on its own: only its client can commit or roll back.")
				f.Evidence = append(f.Evidence,
					ev("root pid", root.PID, "", "pg_blocking_pids"),
					ev("state", *root.State, "", "pg_stat_activity.state"),
					ev("transitively blocked (unique)", root.Transitive, "sessions", "pg_blocking_pids"),
					ev("consecutive samples", m, "", "locklens history"))
				if root.TxAge != nil {
					f.Evidence = append(f.Evidence, ev("transaction age", *root.TxAge, "s", "pg_stat_activity.xact_start"))
				}
				f.Explanation = "A session holds locks inside an open transaction while doing nothing on the server, and other sessions wait for it."
				f.PossibleExplanations = []string{
					"The application started a transaction and is waiting on something else (an HTTP call, user input) before committing.",
					"A code path is missing COMMIT/ROLLBACK, or an error left the transaction open.",
					"A connection pool returned a connection with an open transaction.",
				}
				f.NextChecks = []string{
					"Identify the client from application_name, user, and client address.",
					"Check the application's transaction handling around the last statement.",
					"Consider idle_in_transaction_session_timeout as a safety net (a configuration decision LockLens does not make).",
				}
				f.Limitations = "LockLens only observes; it does not terminate the session. The state is sampled, so a session that briefly ran statements between samples may still appear idle."
				if restricted {
					f.Limitations += visibilityLimitation
				}
				out = append(out, f)
			}
		}
	}
	return out
}
