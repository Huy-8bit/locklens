package rules

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"time"
)

func vacuumRules(in Input, th Thresholds) []Finding {
	t := in.View.Tables
	if t == nil {
		why := "Table statistics have not been collected yet (slow interval, about 5 minutes)."
		return []Finding{insufficient(RuleDeadTuples, in.Now, why), insufficient(RuleXIDAge, in.Now, why)}
	}
	var out []Finding
	longTx := ""
	if in.View.Report != nil && in.View.Report.Analysis.Summary.LongTransactions > 0 {
		longTx = fmt.Sprintf("%d long-running transaction(s) are open now; they can keep VACUUM from removing dead rows.", in.View.Report.Analysis.Summary.LongTransactions)
	}

	type cand struct {
		i     int
		dead  int64
		ratio float64
	}
	var cands []cand
	for i, s := range t.Tables {
		if s.DeadTuples == nil || s.LiveTuples == nil {
			continue
		}
		dead, live := *s.DeadTuples, *s.LiveTuples
		if dead < th.DeadTuplesMin || dead+live == 0 {
			continue
		}
		r := float64(dead) / float64(dead+live)
		if r < th.DeadTuplesRatio {
			continue
		}
		last := s.LastAutovacuum
		if s.LastVacuum != nil && (last == nil || s.LastVacuum.After(*last)) {
			last = s.LastVacuum
		}
		if last != nil && t.At.Sub(*last).Seconds() < th.DeadTuplesVacuumAgeSeconds {
			continue
		}
		cands = append(cands, cand{i, dead, r})
	}
	slices.SortFunc(cands, func(a, b cand) int { return cmp.Compare(b.dead, a.dead) })
	if len(cands) > 10 {
		cands = cands[:10]
	}
	for _, c := range cands {
		s := t.Tables[c.i]
		name := s.Schema + "." + s.Name
		f := newFinding(RuleDeadTuples, "rel:"+strconv.FormatUint(uint64(s.RelID), 10), Warning,
			fmt.Sprintf("Table %s has an estimated %.0f%% dead tuples", name, c.ratio*100), t.At, t.At)
		lastStr := "never (since statistics were last reset)"
		if s.LastAutovacuum != nil || s.LastVacuum != nil {
			lv := s.LastAutovacuum
			if s.LastVacuum != nil && (lv == nil || s.LastVacuum.After(*lv)) {
				lv = s.LastVacuum
			}
			lastStr = lv.UTC().Format(time.RFC3339)
		}
		f.Observed = append(f.Observed, fmt.Sprintf("pg_stat_user_tables estimates %d dead and %d live tuples; last vacuum: %s.", *s.DeadTuples, *s.LiveTuples, lastStr))
		f.Evidence = append(f.Evidence,
			ev("table", name, "", "pg_stat_user_tables"),
			ev("database", s.DatName, "", "current_database()"),
			ev("estimated dead tuples", *s.DeadTuples, "", "pg_stat_user_tables.n_dead_tup"),
			ev("estimated live tuples", *s.LiveTuples, "", "pg_stat_user_tables.n_live_tup"),
			ev("dead tuple ratio (estimate)", c.ratio, "ratio", "derived"),
			ev("last vacuum", lastStr, "", "pg_stat_user_tables"))
		f.Explanation = "Many row versions are dead and not yet reclaimed. This is an estimate from statistics, not a measurement of bloat."
		f.PossibleExplanations = []string{"Autovacuum thresholds are too high for this table's update rate.", "Autovacuum cannot keep up (worker count, cost limits).", "Long-running transactions or replication slots hold back the xmin horizon."}
		if longTx != "" {
			f.PossibleExplanations = append(f.PossibleExplanations, longTx)
		}
		f.NextChecks = []string{"Check autovacuum activity (vacuum progress) and per-table settings.", "Check long transactions and replication slots.", "Measure bloat precisely with pgstattuple if needed (LockLens does not run it)."}
		f.Limitations = "n_dead_tup and n_live_tup are estimates maintained by the statistics system; they can lag and are reset with statistics. A high ratio does not equal measured bloat. LockLens never runs VACUUM."
		out = append(out, f)
	}

	freezeMax, err := strconv.ParseFloat(t.Settings["autovacuum_freeze_max_age"], 64)
	if err != nil || freezeMax <= 0 {
		out = append(out, insufficient(RuleXIDAge, in.Now, "autovacuum_freeze_max_age is not readable."))
		return out
	}
	for _, d := range t.Databases {
		if d.XIDAge == nil {
			continue
		}
		frac := float64(*d.XIDAge) / freezeMax
		if frac < th.XIDAgeWarnFraction {
			continue
		}
		sev := Warning
		if frac >= th.XIDAgeCriticalFraction {
			sev = Critical
		}
		f := newFinding(RuleXIDAge, "db:"+d.DatName, sev,
			fmt.Sprintf("Database %s transaction ID age is %.0f%% of autovacuum_freeze_max_age", d.DatName, frac*100), t.At, t.At)
		f.Observed = append(f.Observed, fmt.Sprintf("age(datfrozenxid) is %d; autovacuum_freeze_max_age is %.0f.", *d.XIDAge, freezeMax))
		f.Evidence = append(f.Evidence, ev("database", d.DatName, "", "pg_database"),
			ev("xid age", *d.XIDAge, "transactions", "age(pg_database.datfrozenxid)"),
			ev("autovacuum_freeze_max_age", freezeMax, "transactions", "pg_settings"))
		f.Explanation = "Old transaction IDs must be frozen by VACUUM. Beyond autovacuum_freeze_max_age, anti-wraparound autovacuums run; far beyond it the server eventually stops accepting new transactions to prevent wraparound."
		f.PossibleExplanations = []string{"Large tables not frozen recently.", "Long transactions, prepared transactions, or slots preventing freezing."}
		if longTx != "" {
			f.PossibleExplanations = append(f.PossibleExplanations, longTx)
		}
		f.NextChecks = []string{"Find tables with the highest frozen XID age in the table health view.", "Check prepared transactions and replication slots."}
		f.Limitations = "The age is relative to the current transaction counter at collection time (slow interval)."
		out = append(out, f)
	}
	return out
}
