package rules

import (
	"fmt"
	"time"

	"github.com/locklens/locklens/internal/domain/collector"
)

// windowTotal integrates a metric over (from, to]. For rate metrics (perSecond)
// each value is multiplied by the time since the previous point; delta
// metrics are summed. coverage is the share of the window backed by known
// values; unknown (NULL) values and missing points reduce it.
func windowTotal(h []collector.TimedValue, from, to time.Time, perSecond bool) (total, coverage float64) {
	var prev time.Time
	var covered float64
	for _, p := range h {
		if !p.At.After(from) || p.At.After(to) {
			prev = p.At
			continue
		}
		dt := 0.0
		if !prev.IsZero() {
			start := prev
			if start.Before(from) {
				start = from
			}
			dt = p.At.Sub(start).Seconds()
		}
		prev = p.At
		if p.Value == nil {
			continue
		}
		covered += dt
		if perSecond {
			total += *p.Value * dt
		} else {
			total += *p.Value
		}
	}
	w := to.Sub(from).Seconds()
	if w <= 0 {
		return total, 0
	}
	return total, min(covered/w, 1)
}

const minCounterCoverage = 0.7

func tempFilesRule(in Input, th Thresholds) []Finding {
	h, ok := in.View.Points["temp.bytes.per_s"]
	if !ok || len(h) == 0 {
		return []Finding{insufficient(RuleTempFiles, in.Now, "No temporary-file counters have been collected yet.")}
	}
	win := time.Duration(th.TempWindowSeconds * float64(time.Second))
	cur, cc := windowTotal(h, in.Now.Add(-win), in.Now, true)
	prev, pc := windowTotal(h, in.Now.Add(-2*win), in.Now.Add(-win), true)
	if cc < minCounterCoverage || pc < minCounterCoverage {
		return []Finding{insufficient(RuleTempFiles, in.Now, fmt.Sprintf("Temporary-file rates cover %.0f%% and %.0f%% of the two %s windows (collection gaps, restarts, or statistics resets).", cc*100, pc*100, fmtDuration(win.Seconds())))}
	}
	if cur < th.TempMinBytes || (prev > 0 && cur/prev < th.TempRatio) {
		return nil
	}
	f := newFinding(RuleTempFiles, "_", Warning, fmt.Sprintf("Temporary-file writes increased to %s per %s", fmtBytes(cur), fmtDuration(win.Seconds())), in.Now.Add(-2*win), in.Now)
	f.Observed = append(f.Observed, fmt.Sprintf("Temporary files written: %s in the current window versus %s in the previous one (pg_stat_database.temp_bytes).", fmtBytes(cur), fmtBytes(prev)))
	f.Evidence = append(f.Evidence, ev("temp bytes (current window)", cur, "bytes", "pg_stat_database.temp_bytes"),
		ev("temp bytes (previous window)", prev, "bytes", "pg_stat_database.temp_bytes"),
		ev("window length", win.Seconds(), "s", "rule configuration"))
	f.Explanation = "Sorts, hashes, or materialisations spilled to disk more than before."
	f.PossibleExplanations = []string{"Queries processing more data than work_mem allows (see temp blocks in the workload view).", "A plan change from an in-memory to a disk-based strategy.", "A batch job or report running now."}
	f.NextChecks = []string{"Sort the workload view by temporary blocks written.", "Check the queries active during the window."}
	f.Limitations = "temp_bytes is cumulative per database and counts all temporary files, including those of maintenance commands. It cannot attribute spills to queries; pg_stat_statements temp blocks can, when available."
	return []Finding{f}
}

func deadlocksRule(in Input, th Thresholds) []Finding {
	h, ok := in.View.Points["deadlocks.delta"]
	if !ok || len(h) == 0 {
		return []Finding{insufficient(RuleDeadlocks, in.Now, "No deadlock counters have been collected yet.")}
	}
	win := time.Duration(th.DeadlockWindowSeconds * float64(time.Second))
	total, cov := windowTotal(h, in.Now.Add(-win), in.Now, false)
	if total <= 0 {
		if cov < minCounterCoverage {
			return []Finding{insufficient(RuleDeadlocks, in.Now, fmt.Sprintf("Deadlock counters cover only %.0f%% of the window.", cov*100))}
		}
		return nil
	}
	f := newFinding(RuleDeadlocks, "_", Warning, fmt.Sprintf("%.0f deadlock(s) detected by PostgreSQL in the last %s", total, fmtDuration(win.Seconds())), in.Now.Add(-win), in.Now)
	f.Observed = append(f.Observed, fmt.Sprintf("pg_stat_database.deadlocks increased by %.0f.", total))
	f.Evidence = append(f.Evidence, ev("deadlocks", total, "", "pg_stat_database.deadlocks"), ev("window length", win.Seconds(), "s", "rule configuration"))
	f.Explanation = "PostgreSQL's deadlock detector aborted transactions that waited for each other in a cycle. Each aborted transaction returned an error to its client."
	f.PossibleExplanations = []string{"Transactions lock the same rows or tables in different orders.", "Foreign-key checks or triggers take locks in an unexpected order."}
	f.NextChecks = []string{"Search the server log for 'deadlock detected' entries (they name the statements); log_lock_waits helps too.", "Review the order in which the application updates rows."}
	f.Limitations = "The counter says how many deadlocks occurred, not which statements were involved; the server log has the details. Sampled blocking graphs rarely capture a deadlock because PostgreSQL resolves it within deadlock_timeout."
	return []Finding{f}
}

func archiverRule(in Input, th Thresholds) []Finding {
	h, ok := in.View.Points["archiver.failed.delta"]
	if !ok || len(h) == 0 {
		return []Finding{insufficient(RuleArchiverFailures, in.Now, "No archiver counters have been collected yet.")}
	}
	win := time.Duration(th.ArchiverWindowSeconds * float64(time.Second))
	total, _ := windowTotal(h, in.Now.Add(-win), in.Now, false)
	if total <= 0 {
		return nil
	}
	f := newFinding(RuleArchiverFailures, "_", Warning, fmt.Sprintf("WAL archiving failed %.0f time(s) in the last %s", total, fmtDuration(win.Seconds())), in.Now.Add(-win), in.Now)
	f.Observed = append(f.Observed, fmt.Sprintf("pg_stat_archiver.failed_count increased by %.0f.", total))
	f.Evidence = append(f.Evidence, ev("archive failures", total, "", "pg_stat_archiver.failed_count"))
	if r := in.View.Replication; r != nil && r.Archiver != nil {
		f.Evidence = append(f.Evidence, ev("last failed WAL", strOr(r.Archiver.LastFailedWAL, "unknown"), "", "pg_stat_archiver.last_failed_wal"),
			ev("archive_mode", r.Archiver.ArchiveMode, "", "pg_settings"))
	}
	f.Explanation = "archive_command or archive_library returned errors. PostgreSQL keeps the WAL until archiving succeeds, so pg_wal can grow."
	f.PossibleExplanations = []string{"The archive destination is unreachable or full.", "Credentials or permissions of the archive command changed."}
	f.NextChecks = []string{"Check the server log for archive command errors.", "Check free space of the WAL directory (host metrics, if configured)."}
	f.Limitations = "Counters do not say why archiving failed; the server log does."
	return []Finding{f}
}
