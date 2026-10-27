package rules

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/domain/baseline"
	"github.com/locklens/locklens/internal/domain/finding"
	"github.com/locklens/locklens/internal/domain/observe"
)

// maxPerRule bounds the findings one statement rule reports per evaluation
// (the strongest first), so a fleet-wide change cannot flood the store.
const maxPerRule = 5

// stmtWindow is one statement's current window with what it is compared to.
type stmtWindow struct {
	key   observe.QueryKey
	meta  observe.QueryDelta
	cur   WindowAgg
	prev  WindowAgg
	vals  map[baseline.Measure]float64 // current measures
	prof  baseline.Profile
	basis baseline.Basis
	warm  bool // the baseline has enough points to be trusted
	total int  // covered points of the whole baseline
	days  int
}

// measures returns a window aggregate's measures (rates over covered seconds).
func measures(a WindowAgg, covered float64, minCalls int64) map[baseline.Measure]float64 {
	return baseline.Point{Covered: covered, Calls: a.Calls, TotalMS: a.TotalMS, Rows: a.Rows, BlkRead: a.BlkRead, BlkHit: a.BlkHit,
		TempW: a.TempW}.Values(minCalls)
}

// hit is one statement that a rule flags.
type hit struct {
	w        stmtWindow
	score    float64 // ordering only: how unusual
	title    string
	sev      Severity
	observed string
	metrics  map[string]float64
	basis    string // what the value was compared with
	conf     *finding.Confidence
	trigger  bool
	extraEv  []Evidence
}

func workloadRules(in Input, th Thresholds) []Finding {
	caps := in.View.Capabilities
	ids := []string{RuleMeanRegression, RuleCallVolume}
	if caps == nil || !caps.StatStatements {
		why := "pg_stat_statements is not available for this target."
		if caps != nil && caps.StatementsReason != "" {
			why = "pg_stat_statements is not available: " + caps.StatementsReason + "."
		}
		return insufficientAll(ids, in.Now, why)
	}
	win := time.Duration(th.WorkloadWindowSeconds * float64(time.Second))
	cur := aggregate(in.View.Statements, in.Now.Add(-win), in.Now)
	prev := aggregate(in.View.Statements, in.Now.Add(-2*win), in.Now.Add(-win))
	need := th.WorkloadMinCoverage * win.Seconds()
	if cur.covered < need {
		why := fmt.Sprintf("The current %s window needs %.0f%% coverage; observed %.0f%%. Collection may have started recently or had gaps.",
			fmtDuration(win.Seconds()), th.WorkloadMinCoverage*100, 100*cur.covered/win.Seconds())
		return insufficientAll(ids, in.Now, why)
	}
	prevOK := prev.covered >= need

	limitations := "Based on pg_stat_statements deltas between collections: there are no per-execution samples, so only means over periods (not percentiles) are compared. queryid is normalised per database, user, and top-level flag, and is not stable across major versions."
	if cur.dealloc+prev.dealloc > 0 {
		limitations += fmt.Sprintf(" %d entries were evicted (pg_stat_statements.max) during the windows, so some activity is missing.", cur.dealloc+prev.dealloc)
	}
	if cur.resets+prev.resets > 0 {
		limitations += " Statistics were reset during the windows."
	}
	if cur.truncated+prev.truncated > 0 {
		limitations += " Some low-impact entries were not stored because of the per-collection cap."
	}

	var totalLoad float64 // execution time per second of all statements
	for _, a := range cur.aggs {
		totalLoad += a.TotalMS
	}
	totalLoad /= cur.covered

	windows := make([]stmtWindow, 0, len(cur.aggs))
	for k, a := range cur.aggs {
		if a.Calls == 0 && a.TotalMS == 0 {
			continue
		}
		w := stmtWindow{key: k, meta: cur.meta[k], cur: a, prev: prev.aggs[k], vals: measures(a, cur.covered, 1)}
		if b, ok := in.Baselines[k]; ok {
			// The profile of the hour the current window is centred on.
			w.prof, w.basis = b.For(in.Now.Add(-win / 2))
			w.total, w.days = b.All.Points, b.Days
			w.warm = w.total >= th.BaselineWarmupPoints && w.prof.Points >= baseline.MinProfilePoints
		}
		windows = append(windows, w)
	}

	ctx := anomalyCtx{in: in, th: th, win: win, cur: cur, prev: prev, prevOK: prevOK, totalLoad: totalLoad}
	var out []Finding
	emit := func(rule string, hits []hit, explain func(*Finding, hit)) {
		// Strongest first; ties by key, so the same data gives the same findings.
		slices.SortFunc(hits, func(a, b hit) int {
			if c := cmp.Compare(b.score, a.score); c != 0 {
				return c
			}
			return cmp.Compare(statementKey(a.w.key), statementKey(b.w.key))
		})
		if len(hits) > maxPerRule {
			hits = hits[:maxPerRule]
		}
		for _, h := range hits {
			f := newFinding(rule, statementKey(h.w.key), h.sev, h.title, in.Now.Add(-2*win), in.Now)
			f.Trigger = h.trigger
			f.Observed = append(f.Observed, h.observed)
			f.Confidence = h.conf
			f.Metrics = h.metrics
			ctx.evidence(&f, h)
			f.Limitations = limitations
			explain(&f, h)
			out = append(out, f)
		}
	}
	var lat, vol, load, io, pat, n1 []hit
	for _, w := range windows {
		if h, ok := ctx.latency(w); ok {
			lat = append(lat, h)
		}
		if h, ok := ctx.volume(w); ok {
			vol = append(vol, h)
		}
		if h, ok := ctx.load(w); ok {
			load = append(load, h)
		}
		if h, ok := ctx.io(w); ok {
			io = append(io, h)
		}
		if h, ok := ctx.pattern(w); ok {
			pat = append(pat, h)
		}
		if h, ok := ctx.nPlusOne(w); ok {
			n1 = append(n1, h)
		}
	}
	emit(RuleMeanRegression, lat, func(f *Finding, h hit) {
		f.Explanation = "Each execution of this statement takes longer than usual; this is different from the statement simply running more often."
		f.PossibleExplanations = []string{"A different execution plan (for example after statistics changed or an index was dropped).",
			"Lock waits or I/O contention during execution.", "More data processed per call (compare rows per call)."}
		f.NextChecks = []string{"Open the statement's page: compare it with its baseline and read the correlated evidence.",
			"Check for blocking, temporary-file activity, or a plan change around the time it started (EXPLAIN is not run automatically)."}
	})
	emit(RuleCallVolume, vol, func(f *Finding, h hit) {
		f.Explanation = "The statement is executed much more often than usual; its total load can grow even when each call is as fast as before."
		f.PossibleExplanations = []string{"More application traffic.", "A retry loop or an N+1 query pattern.", "A cache in front of the database stopped working."}
		f.NextChecks = []string{"Check the application's traffic and deployments around the start.", "Compare the statement's calls with other statements of the same application."}
	})
	emit(RuleLoadIncrease, load, func(f *Finding, h hit) {
		f.Explanation = "The statement consumes much more execution time per second than usual, from slower calls, more calls, or both. This is the time the server spends on it."
		f.PossibleExplanations = []string{"Each call got slower (see the latency evidence).", "The statement runs more often (see the calls evidence)."}
		f.NextChecks = []string{"Open the statement's page to see which of time per call and calls changed, and the correlated evidence."}
	})
	emit(RuleIORegression, io, func(f *Finding, h hit) {
		f.Explanation = "Each call reads many more blocks from outside shared buffers, or writes more temporary data, than usual."
		f.PossibleExplanations = []string{"A plan that scans more data (for example a sequential scan instead of an index scan).",
			"The working set no longer fits in shared buffers.", "Sorts or hashes spilling to disk (work_mem)."}
		f.NextChecks = []string{"Compare the plan with an earlier one (statement page, plan history).", "Check the cache hit ratio and temporary files of the instance."}
	})
	emit(RulePatternChange, pat, func(f *Finding, h hit) {
		f.Explanation = "The rows each call returns or changes differ strongly from the statement's usual behaviour."
		f.PossibleExplanations = []string{"Different parameters or a changed query in the application.", "Data growth or deletion in the tables the statement reads."}
		f.NextChecks = []string{"Check deployments and data changes around the start.", "Compare rows per call with the statement's history on its page."}
	})
	emit(RuleNPlusOne, n1, func(f *Finding, h hit) {
		f.Explanation = "A fast statement returning at most one row is executed very often. This is a common sign of an N+1 pattern (one query per item of a list) in an application, but PostgreSQL alone cannot prove it: a busy key lookup looks the same."
		f.PossibleExplanations = []string{"An N+1 pattern: a loop that runs one query per row of an earlier result.", "A legitimately hot key lookup (for example sessions or feature flags)."}
		f.NextChecks = []string{"Find the code path that runs this statement and check whether it runs inside a loop.", "Consider batching (IN / ANY) or caching if it does."}
	})
	return out
}

func insufficientAll(ids []string, now time.Time, why string) []Finding {
	out := make([]Finding, len(ids))
	for i, id := range ids {
		out[i] = insufficient(id, now, why)
	}
	return out
}

// statementKey is the finding key of a statement rule.
func statementKey(k observe.QueryKey) string {
	return fmt.Sprintf("q:%d:%d:%d:%t:%d", k.ServerMajor, k.DBID, k.UserID, k.TopLevel, k.QueryID)
}

type anomalyCtx struct {
	in        Input
	th        Thresholds
	win       time.Duration
	cur, prev windowStats
	prevOK    bool
	totalLoad float64
}

// compare tests a current value against the baseline profile of a measure:
// it must be at least ratio times the median and AnomalySensitivity robust
// deviations above it. The floor keeps a constant history (MAD 0) from
// making tiny changes significant.
func (c anomalyCtx) compare(w stmtWindow, m baseline.Measure, minRatio, floor float64) (cur, med, z, r float64, ok bool) {
	st, has := w.prof.Stats[m]
	v, hasV := w.vals[m]
	if !w.warm || !has || !hasV || st.N < baseline.MinProfilePoints || st.Median <= 0 {
		return 0, 0, 0, 0, false
	}
	z = st.Z(v, math.Max(floor, 0.05*st.Median))
	r = v / st.Median
	return v, st.Median, z, r, z >= c.th.AnomalySensitivity && r >= minRatio
}

// confidence grades the evidence of a statement finding.
func (c anomalyCtx) confidence(w stmtWindow, viaBaseline bool) *finding.Confidence {
	var reasons []string
	score := 0
	coverage := c.cur.covered / c.win.Seconds()
	if viaBaseline {
		score += 2
		reasons = append(reasons, fmt.Sprintf("compared with %d points of its own history (%s; %d points over %d day(s) in all)", w.prof.Points, w.basis, w.total, w.days))
		if w.basis == baseline.BasisHourOfDay {
			score++
			reasons = append(reasons, "the same hour of the day, so daily patterns are accounted for")
		}
		if w.total >= 2*c.th.BaselineWarmupPoints {
			score++
		}
	} else {
		reasons = append(reasons, "compared only with the previous window (no warm baseline yet)")
	}
	if w.cur.Calls >= 4*c.th.RegressionMinCalls {
		score++
		reasons = append(reasons, fmt.Sprintf("%d calls in the current window", w.cur.Calls))
	} else {
		reasons = append(reasons, fmt.Sprintf("only %d calls in the current window", w.cur.Calls))
	}
	if coverage >= 0.95 {
		score++
	} else {
		reasons = append(reasons, fmt.Sprintf("the window is %.0f%% covered by collections", coverage*100))
	}
	level := finding.ConfidenceLow
	switch {
	case score >= 5:
		level = finding.ConfidenceHigh
	case score >= 3:
		level = finding.ConfidenceMedium
	}
	return &finding.Confidence{Level: level, Reasons: reasons}
}

// baseMetrics are the measurements every statement finding carries.
func (c anomalyCtx) baseMetrics(w stmtWindow) map[string]float64 {
	m := map[string]float64{"window_seconds": c.win.Seconds(), "calls": float64(w.cur.Calls)}
	for k, v := range w.vals {
		m[string(k)] = v
	}
	if c.totalLoad > 0 {
		m["share_of_load"] = w.vals[baseline.LoadMSPerS] / c.totalLoad
	}
	if w.warm {
		for k, st := range w.prof.Stats {
			m["baseline_"+string(k)] = st.Median
		}
	}
	if st, ok := w.prof.Stats[baseline.LoadMSPerS]; ok && w.warm {
		m["baseline_load_ms_per_s"] = st.Median
		m["added_load_ms_per_s"] = math.Max(0, w.vals[baseline.LoadMSPerS]-st.Median)
	} else if c.prevOK {
		pl := w.prev.TotalMS / c.prev.covered
		m["previous_load_ms_per_s"] = pl
		m["added_load_ms_per_s"] = math.Max(0, w.vals[baseline.LoadMSPerS]-pl)
	}
	return m
}

func (c anomalyCtx) latency(w stmtWindow) (hit, bool) {
	th := c.th
	if w.cur.Calls < th.RegressionMinCalls {
		return hit{}, false
	}
	if cur, med, z, r, ok := c.compare(w, baseline.MeanMS, th.RegressionMeanRatio, th.RegressionMinDeltaMS/th.AnomalySensitivity); w.warm && w.prof.Stats[baseline.MeanMS].N >= baseline.MinProfilePoints {
		if !ok || cur-med < th.RegressionMinDeltaMS {
			return hit{}, false
		}
		m := c.baseMetrics(w)
		m["baseline_mean_ms"], m["ratio"], m["z"] = med, r, z
		sev := Warning
		if r >= th.RegressionCriticalRatio {
			sev = Critical
		}
		return hit{w: w, score: z, sev: sev, trigger: true, metrics: m, basis: "baseline", conf: c.confidence(w, true),
			title:    fmt.Sprintf("Query %d is %.1fx slower per call than usual", w.key.QueryID, r),
			observed: fmt.Sprintf("Time per call is %.2f ms, against a usual %.2f ms (median of %s, %.1f robust deviations above it).", cur, med, w.basis, z)}, true
	}
	// No warm baseline: the previous window, as before.
	if !c.prevOK {
		return hit{}, false
	}
	cmpr := Compare(w.prev, w.cur, th)
	if !cmpr.MeanRegression {
		return hit{}, false
	}
	m := c.baseMetrics(w)
	pm, cm := *w.prev.MeanMS(), *w.cur.MeanMS()
	m["previous_mean_ms"], m["ratio"] = pm, *cmpr.MeanRatio
	return hit{w: w, score: *cmpr.MeanRatio, sev: Warning, trigger: true, metrics: m, basis: "previous window", conf: c.confidence(w, false),
		title:    fmt.Sprintf("Query %d is %.1fx slower per call", w.key.QueryID, *cmpr.MeanRatio),
		observed: fmt.Sprintf("Mean execution time rose from %.2f ms to %.2f ms per call between two %s windows, with %d and %d calls.", pm, cm, fmtDuration(c.win.Seconds()), w.prev.Calls, w.cur.Calls)}, true
}

func (c anomalyCtx) volume(w stmtWindow) (hit, bool) {
	th := c.th
	if w.cur.Calls < th.VolumeMinCalls {
		return hit{}, false
	}
	if cur, med, z, r, ok := c.compare(w, baseline.CallsPerSec, th.VolumeCallsRatio, 0.01); w.warm {
		if !ok {
			return hit{}, false
		}
		m := c.baseMetrics(w)
		m["baseline_calls_per_s"], m["ratio"], m["z"] = med, r, z
		sev, title := Info, fmt.Sprintf("Query %d runs %.1fx more often than usual", w.key.QueryID, r)
		if r >= 3*th.VolumeCallsRatio {
			sev, title = Warning, fmt.Sprintf("Query storm: query %d runs %.1fx more often than usual", w.key.QueryID, r)
		}
		return hit{w: w, score: z, sev: sev, metrics: m, basis: "baseline", conf: c.confidence(w, true), title: title,
			observed: fmt.Sprintf("%.2f calls per second, against a usual %.2f (median of %s).", cur, med, w.basis)}, true
	}
	if !c.prevOK {
		return hit{}, false
	}
	cmpr := Compare(w.prev, w.cur, th)
	if !cmpr.VolumeIncrease {
		return hit{}, false
	}
	m := c.baseMetrics(w)
	m["previous_calls"], m["ratio"] = float64(w.prev.Calls), *cmpr.CallsRatio
	return hit{w: w, score: *cmpr.CallsRatio, sev: Info, metrics: m, basis: "previous window", conf: c.confidence(w, false),
		title:    fmt.Sprintf("Query %d runs %.1fx more often", w.key.QueryID, *cmpr.CallsRatio),
		observed: fmt.Sprintf("Calls rose from %d to %d between two %s windows.", w.prev.Calls, w.cur.Calls, fmtDuration(c.win.Seconds()))}, true
}

func (c anomalyCtx) load(w stmtWindow) (hit, bool) {
	th := c.th
	m := c.baseMetrics(w)
	added := m["added_load_ms_per_s"]
	if added < th.LoadMinAddedMSPerSec {
		return hit{}, false
	}
	if cur, med, z, r, ok := c.compare(w, baseline.LoadMSPerS, th.LoadRatio, th.LoadMinAddedMSPerSec/th.AnomalySensitivity); w.warm {
		if !ok {
			return hit{}, false
		}
		m["ratio"], m["z"] = r, z
		return hit{w: w, score: added, sev: Warning, metrics: m, basis: "baseline", conf: c.confidence(w, true),
			title:    fmt.Sprintf("Query %d uses %.1fx its usual execution time", w.key.QueryID, r),
			observed: fmt.Sprintf("It consumes %.1f ms of execution per second, against a usual %.1f ms (median of %s): %.1f ms/s more.", cur, med, w.basis, added)}, true
	}
	if !c.prevOK || w.prev.TotalMS <= 0 {
		return hit{}, false
	}
	pl := w.prev.TotalMS / c.prev.covered
	r := w.vals[baseline.LoadMSPerS] / pl
	if r < th.LoadRatio {
		return hit{}, false
	}
	m["ratio"] = r
	return hit{w: w, score: added, sev: Warning, metrics: m, basis: "previous window", conf: c.confidence(w, false),
		title:    fmt.Sprintf("Query %d uses %.1fx more execution time", w.key.QueryID, r),
		observed: fmt.Sprintf("Execution time per second rose from %.1f ms to %.1f ms between two %s windows.", pl, w.vals[baseline.LoadMSPerS], fmtDuration(c.win.Seconds()))}, true
}

func (c anomalyCtx) io(w stmtWindow) (hit, bool) {
	th := c.th
	if w.cur.Calls < th.RegressionMinCalls {
		return hit{}, false
	}
	if cur, med, z, r, ok := c.compare(w, baseline.ReadPerCall, th.IORatio, th.IOMinReadsPerCall/th.AnomalySensitivity); ok && cur >= th.IOMinReadsPerCall {
		m := c.baseMetrics(w)
		m["baseline_reads_per_call"], m["ratio"], m["z"] = med, r, z
		return hit{w: w, score: z, sev: Warning, metrics: m, basis: "baseline", conf: c.confidence(w, true),
			title:    fmt.Sprintf("Query %d reads %.1fx more blocks per call than usual", w.key.QueryID, r),
			observed: fmt.Sprintf("%.0f shared blocks read per call (not found in shared buffers), against a usual %.0f (median of %s).", cur, med, w.basis)}, true
	}
	if cur, med, z, r, ok := c.compare(w, baseline.TempPerCall, th.IORatio, 1); ok && cur >= 1 {
		m := c.baseMetrics(w)
		m["baseline_temp_per_call"], m["ratio"], m["z"] = med, r, z
		return hit{w: w, score: z, sev: Warning, metrics: m, basis: "baseline", conf: c.confidence(w, true),
			title:    fmt.Sprintf("Query %d writes %.1fx more temporary data per call than usual", w.key.QueryID, r),
			observed: fmt.Sprintf("%.0f temporary blocks written per call, against a usual %.0f (median of %s).", cur, med, w.basis)}, true
	}
	return hit{}, false
}

func (c anomalyCtx) pattern(w stmtWindow) (hit, bool) {
	th := c.th
	st, has := w.prof.Stats[baseline.RowsPerCall]
	v, hasV := w.vals[baseline.RowsPerCall]
	if !w.warm || !has || !hasV || st.N < baseline.MinProfilePoints || w.cur.Calls < th.RegressionMinCalls {
		return hit{}, false
	}
	floor := math.Max(1, 0.05*st.Median)
	z := st.Z(v, floor)
	up := st.Median > 0 && v/st.Median >= th.PatternRowsRatio && z >= th.AnomalySensitivity
	down := v > 0 && st.Median/v >= th.PatternRowsRatio && -z >= th.AnomalySensitivity
	if !up && !down {
		return hit{}, false
	}
	m := c.baseMetrics(w)
	m["baseline_rows_per_call"], m["z"] = st.Median, z
	dir := "more"
	r := v / math.Max(st.Median, 1e-9)
	if down {
		dir, r = "fewer", st.Median/v
	}
	m["ratio"] = r
	return hit{w: w, score: math.Abs(z), sev: Info, metrics: m, basis: "baseline", conf: c.confidence(w, true),
		title:    fmt.Sprintf("Query %d handles %.1fx %s rows per call than usual", w.key.QueryID, r, dir),
		observed: fmt.Sprintf("%.1f rows per call, against a usual %.1f (median of %s).", v, st.Median, w.basis)}, true
}

func (c anomalyCtx) nPlusOne(w stmtWindow) (hit, bool) {
	th := c.th
	cps := w.vals[baseline.CallsPerSec]
	mean, okM := w.vals[baseline.MeanMS]
	rows := w.vals[baseline.RowsPerCall]
	if !okM || cps < th.NPlusOneMinCallsPerSec || mean > th.NPlusOneMaxMeanMS || rows > th.NPlusOneMaxRowsPerCall {
		return hit{}, false
	}
	if w.meta.Command != "" && w.meta.Command != observe.CommandSelect {
		return hit{}, false
	}
	if w.meta.Text != nil && !strings.Contains(*w.meta.Text, "$") {
		return hit{}, false // without parameters it is not a per-item lookup
	}
	m := c.baseMetrics(w)
	conf := &finding.Confidence{Level: finding.ConfidenceLow, Reasons: []string{
		"PostgreSQL sees statements, not the application loop that issues them", fmt.Sprintf("%.0f calls per second at %.2f ms and %.1f rows per call", cps, mean, rows)}}
	return hit{w: w, score: cps, sev: Info, metrics: m, basis: "pattern", conf: conf,
		title:    fmt.Sprintf("Possible N+1 pattern: query %d runs %.0f times per second", w.key.QueryID, cps),
		observed: fmt.Sprintf("A single-row lookup runs %.0f times per second, %.2f ms each.", cps, mean)}, true
}

// evidence lists what a statement finding compared.
func (c anomalyCtx) evidence(f *Finding, h hit) {
	w := h.w
	f.Evidence = append(f.Evidence,
		ev("queryid", fmt.Sprint(w.key.QueryID), "", "pg_stat_statements"),
		ev("database", w.meta.DatName, "", "pg_stat_statements.dbid"),
		ev("user", w.meta.UserName, "", "pg_stat_statements.userid"),
		ev("top level", w.key.TopLevel, "", "pg_stat_statements.toplevel"),
		ev("compared with", h.basis, "", "rule"),
		ev("calls (current window)", w.cur.Calls, "", "pg_stat_statements deltas"))
	if v, ok := w.vals[baseline.MeanMS]; ok {
		f.Evidence = append(f.Evidence, ev("mean time (current window)", v, "ms", "pg_stat_statements deltas"))
	}
	f.Evidence = append(f.Evidence, ev("execution time per second (current window)", w.vals[baseline.LoadMSPerS], "ms/s", "pg_stat_statements deltas"))
	if h.basis == "baseline" {
		for _, m := range []baseline.Measure{baseline.MeanMS, baseline.CallsPerSec, baseline.LoadMSPerS, baseline.RowsPerCall, baseline.ReadPerCall} {
			if st, ok := w.prof.Stats[m]; ok {
				f.Evidence = append(f.Evidence, ev("baseline "+strings.ReplaceAll(string(m), "_", " ")+" (median ± MAD)", fmt.Sprintf("%.3g ± %.3g", st.Median, st.MAD), "", fmt.Sprintf("%d points, %s", st.N, w.basis)))
			}
		}
	} else if h.basis == "previous window" {
		f.Evidence = append(f.Evidence, ev("calls (previous window)", w.prev.Calls, "", "pg_stat_statements deltas"))
		if pm := w.prev.MeanMS(); pm != nil {
			f.Evidence = append(f.Evidence, ev("mean time (previous window)", *pm, "ms", "pg_stat_statements deltas"))
		}
	}
	f.Evidence = append(f.Evidence, ev("window length", c.win.Seconds(), "s", "rule configuration"))
	f.Evidence = append(f.Evidence, h.extraEv...)
}
