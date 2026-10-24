package rules

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/locklens/locklens/internal/observe"
)

// WindowAgg aggregates pg_stat_statements deltas of one query identity over
// a time window. Mean execution time is TotalMS / Calls within the window,
// computed from deltas; LockLens never reports percentiles because
// pg_stat_statements has no per-execution samples.
type WindowAgg struct {
	Calls   int64   `json:"calls"`
	TotalMS float64 `json:"total_exec_ms"`
	Rows    int64   `json:"rows"`
}

// MeanMS returns the window mean, or nil without calls.
func (w WindowAgg) MeanMS() *float64 {
	if w.Calls <= 0 {
		return nil
	}
	m := w.TotalMS / float64(w.Calls)
	return &m
}

// Comparison of a query identity between two equal-length windows.
type Comparison struct {
	MeanRatio  *float64 `json:"mean_ratio"`
	CallsRatio *float64 `json:"calls_ratio"`
	LoadRatio  *float64 `json:"total_time_ratio"`
	// MeanRegression: each call got slower.
	MeanRegression bool `json:"mean_regression"`
	// VolumeIncrease: many more calls.
	VolumeIncrease bool `json:"volume_increase"`
	// LoadIncrease: more total execution time (from either cause).
	LoadIncrease bool `json:"load_increase"`
	// TooFewCalls is true when either window is below the minimum and no
	// flag can be raised from such a small sample.
	TooFewCalls bool `json:"too_few_calls"`
}

func ratio(a, b float64) *float64 {
	if b <= 0 {
		return nil
	}
	r := a / b
	return &r
}

// Compare classifies the change from prev to cur.
func Compare(prev, cur WindowAgg, th Thresholds) Comparison {
	var c Comparison
	c.CallsRatio = ratio(float64(cur.Calls), float64(prev.Calls))
	c.LoadRatio = ratio(cur.TotalMS, prev.TotalMS)
	pm, cm := prev.MeanMS(), cur.MeanMS()
	if pm != nil && cm != nil {
		c.MeanRatio = ratio(*cm, *pm)
	}
	if prev.Calls < th.RegressionMinCalls || cur.Calls < th.RegressionMinCalls {
		c.TooFewCalls = true
		return c
	}
	if c.MeanRatio != nil && *c.MeanRatio >= th.RegressionMeanRatio && *cm-*pm >= th.RegressionMinDeltaMS {
		c.MeanRegression = true
	}
	if c.CallsRatio != nil && *c.CallsRatio >= th.VolumeCallsRatio && cur.Calls >= th.VolumeMinCalls {
		c.VolumeIncrease = true
	}
	if c.LoadRatio != nil && *c.LoadRatio >= th.VolumeCallsRatio {
		c.LoadIncrease = true
	}
	return c
}

type windowStats struct {
	aggs      map[observe.QueryKey]WindowAgg
	meta      map[observe.QueryKey]observe.QueryDelta
	covered   float64
	resets    int
	dealloc   int64
	truncated int
}

// aggregate sums in-memory statement collections with At in (from, to].
func aggregate(colls []observe.StatementsCollection, from, to time.Time) windowStats {
	w := windowStats{aggs: map[observe.QueryKey]WindowAgg{}, meta: map[observe.QueryKey]observe.QueryDelta{}}
	for _, c := range colls {
		if !c.At.After(from) || c.At.After(to) || c.Interval == nil {
			continue
		}
		w.covered += *c.Interval
		if c.Reset {
			w.resets++
		}
		if c.Dealloc != nil {
			w.dealloc += *c.Dealloc
		}
		w.truncated += c.Truncated
		for _, d := range c.Deltas {
			a := w.aggs[d.Key]
			a.Calls += d.Calls
			a.TotalMS += d.TotalMS
			a.Rows += d.Rows
			w.aggs[d.Key] = a
			w.meta[d.Key] = d
		}
	}
	return w
}

func workloadRules(in Input, th Thresholds) []Finding {
	caps := in.View.Capabilities
	if caps == nil || !caps.StatStatements {
		why := "pg_stat_statements is not available for this target."
		if caps != nil && caps.StatementsReason != "" {
			why = "pg_stat_statements is not available: " + caps.StatementsReason + "."
		}
		return []Finding{insufficient(RuleMeanRegression, in.Now, why), insufficient(RuleCallVolume, in.Now, why)}
	}
	win := time.Duration(th.WorkloadWindowSeconds * float64(time.Second))
	cur := aggregate(in.View.Statements, in.Now.Add(-win), in.Now)
	prev := aggregate(in.View.Statements, in.Now.Add(-2*win), in.Now.Add(-win))
	need := th.WorkloadMinCoverage * win.Seconds()
	if cur.covered < need || prev.covered < need {
		why := fmt.Sprintf("Comparing two %s windows needs %.0f%% coverage of each; observed %.0f%% (current) and %.0f%% (previous). Collection may have started recently or had gaps.",
			fmtDuration(win.Seconds()), th.WorkloadMinCoverage*100, 100*cur.covered/win.Seconds(), 100*prev.covered/win.Seconds())
		return []Finding{insufficient(RuleMeanRegression, in.Now, why), insufficient(RuleCallVolume, in.Now, why)}
	}
	limitations := "Based on pg_stat_statements deltas between collections: there are no per-execution samples, so only window means (not percentiles) are compared. queryid is normalised per database, user, and top-level flag, and is not stable across major versions."
	if cur.dealloc+prev.dealloc > 0 {
		limitations += fmt.Sprintf(" %d entries were evicted (pg_stat_statements.max) during the windows, so some activity is missing.", cur.dealloc+prev.dealloc)
	}
	if cur.resets+prev.resets > 0 {
		limitations += " Statistics were reset during the windows."
	}
	if cur.truncated+prev.truncated > 0 {
		limitations += " Some low-impact entries were not stored because of the per-collection cap."
	}

	type hit struct {
		key  observe.QueryKey
		c    Comparison
		p, n WindowAgg
	}
	var regs, vols []hit
	for k, n := range cur.aggs {
		p := prev.aggs[k]
		c := Compare(p, n, th)
		if c.MeanRegression {
			regs = append(regs, hit{k, c, p, n})
		}
		if c.VolumeIncrease {
			vols = append(vols, hit{k, c, p, n})
		}
	}
	slices.SortFunc(regs, func(a, b hit) int { return cmp.Compare(*b.c.MeanRatio, *a.c.MeanRatio) })
	slices.SortFunc(vols, func(a, b hit) int { return cmp.Compare(*b.c.CallsRatio, *a.c.CallsRatio) })
	if len(regs) > 5 {
		regs = regs[:5]
	}
	if len(vols) > 5 {
		vols = vols[:5]
	}
	from, to := in.Now.Add(-2*win), in.Now
	keyOf := func(k observe.QueryKey) string {
		return fmt.Sprintf("q:%d:%d:%d:%t:%d", k.ServerMajor, k.DBID, k.UserID, k.TopLevel, k.QueryID)
	}
	common := func(f *Finding, h hit) {
		m := cur.meta[h.key]
		f.Evidence = append(f.Evidence,
			ev("queryid", fmt.Sprint(h.key.QueryID), "", "pg_stat_statements"),
			ev("database", m.DatName, "", "pg_stat_statements.dbid"),
			ev("user", m.UserName, "", "pg_stat_statements.userid"),
			ev("top level", h.key.TopLevel, "", "pg_stat_statements.toplevel"),
			ev("calls (previous window)", h.p.Calls, "", "pg_stat_statements deltas"),
			ev("calls (current window)", h.n.Calls, "", "pg_stat_statements deltas"),
			ev("mean time (previous window)", deref(h.p.MeanMS(), 0), "ms", "pg_stat_statements deltas"),
			ev("mean time (current window)", deref(h.n.MeanMS(), 0), "ms", "pg_stat_statements deltas"),
			ev("total time (previous window)", h.p.TotalMS, "ms", "pg_stat_statements deltas"),
			ev("total time (current window)", h.n.TotalMS, "ms", "pg_stat_statements deltas"),
			ev("window length", win.Seconds(), "s", "rule configuration"))
		f.Limitations = limitations
		f.NextChecks = []string{"Open the query in the workload view and compare the windows.", "Check for blocking, temporary-file activity, or a plan change around the time it started (EXPLAIN is not run automatically)."}
	}
	var out []Finding
	for _, h := range regs {
		f := newFinding(RuleMeanRegression, keyOf(h.key), Warning,
			fmt.Sprintf("Query %d is %.1fx slower per call", h.key.QueryID, *h.c.MeanRatio), from, to)
		f.Trigger = true
		f.Observed = append(f.Observed, fmt.Sprintf("Mean execution time rose from %.2f ms to %.2f ms per call between two %s windows, with %d and %d calls.",
			*h.p.MeanMS(), *h.n.MeanMS(), fmtDuration(win.Seconds()), h.p.Calls, h.n.Calls))
		f.Explanation = "Each execution of this statement takes longer than before; this is different from the statement simply running more often."
		f.PossibleExplanations = []string{"A different execution plan (for example after statistics changed or an index was dropped).", "Lock waits or I/O contention during execution.", "More data processed per call (larger result sets: compare rows)."}
		common(&f, h)
		out = append(out, f)
	}
	for _, h := range vols {
		f := newFinding(RuleCallVolume, keyOf(h.key), Info,
			fmt.Sprintf("Query %d runs %.1fx more often", h.key.QueryID, *h.c.CallsRatio), from, to)
		f.Observed = append(f.Observed, fmt.Sprintf("Calls rose from %d to %d between two %s windows.", h.p.Calls, h.n.Calls, fmtDuration(win.Seconds())))
		f.Explanation = "The statement is executed much more often; its total load can grow even when each call is as fast as before."
		f.PossibleExplanations = []string{"More application traffic.", "A retry loop or an N+1 query pattern.", "A cache in front of the database stopped working."}
		common(&f, h)
		out = append(out, f)
	}
	return out
}

// TopQuery is one entry of a workload summary.
type TopQuery struct {
	Key      observe.QueryKey `json:"key"`
	DatName  string           `json:"datname"`
	UserName string           `json:"username"`
	Text     *string          `json:"text,omitempty"`
	WindowAgg
	MeanMS *float64 `json:"mean_exec_ms"`
	Share  *float64 `json:"share_of_total_time"`
}

// TopQueries returns the n identities with the highest total execution time
// in (from, to], with their share of the observed total, and the coverage in
// seconds.
func TopQueries(colls []observe.StatementsCollection, from, to time.Time, n int) ([]TopQuery, float64) {
	w := aggregate(colls, from, to)
	var total float64
	for _, a := range w.aggs {
		total += a.TotalMS
	}
	out := make([]TopQuery, 0, len(w.aggs))
	for k, a := range w.aggs {
		m := w.meta[k]
		q := TopQuery{Key: k, DatName: m.DatName, UserName: m.UserName, Text: m.Text, WindowAgg: a, MeanMS: a.MeanMS()}
		if total > 0 {
			s := a.TotalMS / total
			q.Share = &s
		}
		out = append(out, q)
	}
	slices.SortFunc(out, func(a, b TopQuery) int { return cmp.Compare(b.TotalMS, a.TotalMS) })
	if len(out) > n {
		out = out[:n]
	}
	return out, w.covered
}
