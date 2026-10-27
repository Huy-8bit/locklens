package insight

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/locklens/locklens/internal/domain/finding"
	"github.com/locklens/locklens/internal/domain/rules"
)

// Observed is a finding with when it was first reported.
type Observed struct {
	Finding   finding.Finding `json:"finding"`
	FirstSeen time.Time       `json:"first_seen"`
}

// WaitShift compares average active sessions per wait class in the anomaly
// period (Now) with the period before it (Before), from active session
// sampling. A nil map means no samples were taken in that period.
type WaitShift struct {
	Now    map[string]float64 `json:"now"`
	Before map[string]float64 `json:"before"`
}

// Gap is a period without collection.
type Gap struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	What string    `json:"what"`
}

// Signals is everything known around an anomaly of one statement (or one
// running execution).
type Signals struct {
	At    time.Time // when the investigation looks from
	Since time.Time // when the anomaly began
	// Subject are the findings about the statement under investigation.
	Subject []Observed
	// Others are the target's other findings in the period.
	Others []Observed
	// StatementWaits is the statement's sampled wait profile; nil when
	// sampling is off or the statement has no queryid in pg_stat_activity.
	StatementWaits *WaitShift
	// InstanceWaits is the whole instance's sampled wait profile.
	InstanceWaits *WaitShift
	// OtherRegressions counts other statements of the target whose time per
	// call regressed in the period.
	OtherRegressions int
	Gaps             []Gap
	SamplingEnabled  bool
	// PlanHistory is true when execution plans of the statement were saved.
	PlanHistory bool
}

// Strength of the evidence for a hypothesis. It grades evidence; a
// hypothesis is never a confirmed cause.
const (
	StrengthStrong   = "strong"
	StrengthModerate = "moderate"
	StrengthWeak     = "weak"
)

// Hypothesis is one possible explanation with the evidence for and against it.
type Hypothesis struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Score         float64  `json:"score"` // 0-1, orders hypotheses
	Strength      string   `json:"strength"`
	Supporting    []string `json:"supporting"`
	Contradicting []string `json:"contradicting"`
	NextSteps     []string `json:"next_steps"`
}

// TimelineEntry is one dated event around the anomaly.
type TimelineEntry struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"` // anomaly, finding, gap
	RuleID  string    `json:"rule_id,omitempty"`
	Summary string    `json:"summary"`
}

// Investigation is the correlated view of an anomaly.
type Investigation struct {
	At         time.Time       `json:"at"`
	Since      time.Time       `json:"since"`
	Summary    string          `json:"summary"`
	Hypotheses []Hypothesis    `json:"hypotheses"`
	Timeline   []TimelineEntry `json:"timeline"`
	Missing    []string        `json:"missing_evidence"`
	Disclaimer string          `json:"disclaimer"`
}

// Disclaimer is shown with every investigation.
const Disclaimer = "Hypotheses are ranked by the evidence LockLens collected; none is a confirmed cause. Check them in order before acting."

// hyp accumulates one hypothesis.
type hyp struct {
	Hypothesis
	score float64
}

func (h *hyp) support(w float64, s string, args ...any) {
	h.score += w
	h.Supporting = append(h.Supporting, fmt.Sprintf(s, args...))
}

func (h *hyp) contradict(w float64, s string, args ...any) {
	h.score -= w
	h.Contradicting = append(h.Contradicting, fmt.Sprintf(s, args...))
}

// Correlate ranks hypotheses for an anomaly from its signals.
func Correlate(sig Signals) Investigation {
	inv := Investigation{At: sig.At, Since: sig.Since, Disclaimer: Disclaimer, Hypotheses: []Hypothesis{}}
	subj := map[string]finding.Finding{}
	for _, o := range sig.Subject {
		subj[o.Finding.RuleID] = o.Finding
	}
	others := map[string][]Observed{}
	for _, o := range sig.Others {
		others[o.Finding.RuleID] = append(others[o.Finding.RuleID], o)
	}
	// metric reads one measurement of the subject findings.
	metric := func(name string) (float64, bool) {
		for _, o := range sig.Subject {
			if v, ok := o.Finding.Metrics[name]; ok {
				return v, true
			}
		}
		return 0, false
	}
	ratio := func(m string) (float64, bool) {
		cur, ok1 := metric(m)
		base, ok2 := metric("baseline_" + m)
		if !ok1 || !ok2 || base <= 0 {
			return 0, false
		}
		return cur / base, true
	}
	// when describes when another finding began relative to the anomaly.
	when := func(o Observed) string {
		d := sig.Since.Sub(o.FirstSeen)
		switch {
		case d > time.Minute:
			return fmt.Sprintf("began %s before the anomaly", d.Round(time.Second))
		case d < -time.Minute:
			return fmt.Sprintf("began %s after the anomaly, so it may be a consequence rather than a cause", (-d).Round(time.Second))
		}
		return "began at about the same time as the anomaly"
	}
	// before weighs a correlated finding by temporal precedence.
	before := func(o Observed) float64 {
		if o.FirstSeen.After(sig.Since.Add(time.Minute)) {
			return 0.5
		}
		return 1
	}
	stmtShare := func(class string) (now, prev float64, ok bool) {
		if sig.StatementWaits == nil || sig.StatementWaits.Now == nil {
			return 0, 0, false
		}
		return share(sig.StatementWaits.Now, class), share(sig.StatementWaits.Before, class), true
	}
	_, latency := subj[rules.RuleMeanRegression]
	_, running := subj[rules.RuleLongRunning]
	meanR, hasMeanR := ratio("mean_ms")
	callsR, hasCallsR := ratio("calls_per_s")
	rowsR, hasRowsR := ratio("rows_per_call")
	readsR, hasReadsR := ratio("reads_per_call")
	tempR, hasTempR := ratio("temp_per_call")

	var hs []*hyp
	add := func(id, title string, next ...string) *hyp {
		h := &hyp{Hypothesis: Hypothesis{ID: id, Title: title, Supporting: []string{}, Contradicting: []string{}, NextSteps: next}}
		hs = append(hs, h)
		return h
	}

	// Lock contention.
	lock := add("lock_contention", "Waiting for locks held by other sessions",
		"Open the blocking explorer for the period and find the root blocker.",
		"Check long or idle-in-transaction sessions holding locks on the same tables.")
	if now, prev, ok := stmtShare("Lock"); ok {
		switch {
		case now >= 0.3:
			lock.support(0.5, "%.0f%% of the statement's sampled time waits on locks (%.0f%% before).", 100*now, 100*prev)
		case now < 0.05:
			lock.contradict(0.4, "Only %.0f%% of the statement's sampled time waits on locks.", 100*now)
		}
	}
	for _, id := range []string{rules.RuleBlockingChain, rules.RuleIdleBlocker} {
		for _, o := range others[id] {
			lock.support(0.35*before(o), "%s (%s).", o.Finding.Title, when(o))
		}
	}
	if f, ok := subj[rules.RuleLongRunning]; ok {
		if hasEvidence(f, "blocked by") {
			lock.support(0.5, "The running execution is blocked by another session.")
		}
	}
	for _, o := range others[rules.RuleDeadlocks] {
		lock.support(0.15*before(o), "%s (%s).", o.Finding.Title, when(o))
	}

	// A plan or access path change: more data read per call for the same rows.
	plan := add("plan_change", "A different execution plan or access path",
		"Compare the current plan with a saved one (statement page, plan history); capture one with EXPLAIN (without ANALYZE) if none is saved.",
		"Check whether statistics changed (ANALYZE, autovacuum) or an index was dropped or became invalid.")
	if hasReadsR && readsR >= 2 {
		plan.support(math.Min(0.5, 0.15*math.Log2(readsR)), "Blocks read per call are %.1fx the usual level.", readsR)
	}
	if hasTempR && tempR >= 2 {
		plan.support(0.15, "Temporary data written per call is %.1fx the usual level.", tempR)
	}
	if latency && hasMeanR && meanR >= 2 && hasRowsR && rowsR > 0.5 && rowsR < 2 {
		plan.support(0.25, "Time per call is %.1fx usual while rows per call are unchanged (%.1fx): the same result costs more to produce.", meanR, rowsR)
	}
	if hasRowsR && rowsR >= 2 {
		plan.contradict(0.15, "Rows per call also grew (%.1fx), which points to more data rather than a worse plan.", rowsR)
	}
	if now, _, ok := stmtShare("IO"); ok && now >= 0.3 {
		plan.support(0.15, "%.0f%% of the statement's sampled time waits on I/O.", 100*now)
	}
	if _, ok := subj[rules.RuleIORegression]; ok {
		plan.support(0.15, "The I/O regression rule fired for this statement.")
	}

	// More data per call: data growth or different parameters.
	data := add("data_volume", "More data per call (data growth or different parameters)",
		"Check recent data loads or changes in the tables the statement reads.",
		"Check whether the application now passes broader parameters (for example a larger date range).")
	if hasRowsR && rowsR >= 2 {
		data.support(math.Min(0.6, 0.2*math.Log2(rowsR)), "Rows per call are %.1fx the usual level.", rowsR)
	} else if hasRowsR && rowsR > 0.67 && rowsR < 1.5 {
		data.contradict(0.2, "Rows per call are close to usual (%.1fx).", rowsR)
	}

	// Traffic.
	traffic := add("traffic_increase", "More executions (traffic, retries, or an N+1 pattern)",
		"Compare application traffic and recent deployments with the start of the anomaly.",
		"Look for retry loops or per-item queries in the code path that issues the statement.")
	if hasCallsR && callsR >= 2 {
		traffic.support(math.Min(0.6, 0.2*math.Log2(callsR)), "Calls per second are %.1fx the usual level.", callsR)
		if hasMeanR && meanR < 1.5 {
			traffic.support(0.1, "Time per call is close to usual (%.1fx), so the extra load comes from volume.", meanR)
		}
	} else if hasCallsR && callsR < 1.5 {
		traffic.contradict(0.3, "Calls per second are close to usual (%.1fx).", callsR)
	}
	if _, ok := subj[rules.RuleNPlusOne]; ok {
		traffic.support(0.2, "The statement matches a possible N+1 pattern (fast single-row lookups at a high rate).")
	}

	// Shared resource saturation.
	sat := add("resource_saturation", "Instance-wide resource pressure (CPU, I/O, or memory)",
		"Check host CPU, I/O, and memory for the period (Host page).",
		"Find which statements added the most load in the period (Load page by query).")
	if sig.OtherRegressions >= 2 {
		sat.support(math.Min(0.5, 0.15*float64(sig.OtherRegressions)), "%d other statements became slower per call in the same period.", sig.OtherRegressions)
	} else if latency && sig.OtherRegressions == 0 {
		sat.contradict(0.2, "No other statement became slower per call, so the cause is likely specific to this statement.")
	}
	if iw := sig.InstanceWaits; iw != nil && iw.Now != nil && iw.Before != nil {
		now, prev := total(iw.Now), total(iw.Before)
		if prev > 0 && now >= 1.5*prev {
			sat.support(0.25, "Average active sessions rose from %.1f to %.1f.", prev, now)
		} else if prev > 0 && now < 1.2*prev {
			sat.contradict(0.15, "Average active sessions are about the same as before (%.1f against %.1f).", now, prev)
		}
		if c := share(iw.Now, "CPU"); c >= 0.6 {
			sat.support(0.1, "%.0f%% of sampled time is on CPU (or waiting for it).", 100*c)
		}
	}
	for _, o := range others[rules.RuleConnections] {
		sat.support(0.15*before(o), "%s (%s).", o.Finding.Title, when(o))
	}
	for _, o := range others[rules.RuleTempFiles] {
		sat.support(0.1*before(o), "%s (%s).", o.Finding.Title, when(o))
	}

	// Maintenance: bloat and old snapshots.
	maint := add("maintenance", "Table bloat or VACUUM held back by an old snapshot",
		"Check dead tuples and the last (auto)vacuum of the tables the statement reads (Tables page).",
		"Check transactions that have been open for a long time.")
	for _, id := range []string{rules.RuleDeadTuples, rules.RuleLongTransaction, rules.RuleXIDAge} {
		for _, o := range others[id] {
			maint.support(0.25*before(o), "%s (%s).", o.Finding.Title, when(o))
		}
	}
	if len(maint.Supporting) > 0 && hasReadsR && readsR >= 2 {
		maint.support(0.1, "More blocks read per call is consistent with bloat.")
	}

	// A heavy execution that is still running.
	if running {
		f := subj[rules.RuleLongRunning]
		heavy := add("heavy_execution", "One heavy execution (report, batch job, or migration)",
			"Ask the owner of the application or user whether this run is expected.",
			"Check its wait event: on CPU or I/O it is working, on Lock it is waiting for another session.")
		if !hasEvidence(f, "blocked by") {
			heavy.support(0.3, "The execution is not blocked by another session.")
		}
		if v, ok := f.Metrics["ratio"]; ok && v >= 1 {
			heavy.support(0.1, "It has run %.0fx longer than the statement's usual time per call.", v)
		} else if !ok {
			heavy.support(0.1, "The statement has no baseline, so it may be rare (an ad hoc or batch statement).")
		}
	}

	for _, h := range hs {
		h.Score = math.Round(clamp01(h.score)*100) / 100
		switch {
		case h.Score >= 0.6:
			h.Strength = StrengthStrong
		case h.Score >= 0.3:
			h.Strength = StrengthModerate
		default:
			h.Strength = StrengthWeak
		}
		if len(h.Supporting) > 0 && h.Score > 0 {
			inv.Hypotheses = append(inv.Hypotheses, h.Hypothesis)
		}
	}
	slices.SortStableFunc(inv.Hypotheses, func(a, b Hypothesis) int { return cmp.Compare(b.Score, a.Score) })

	inv.Timeline = timeline(sig)
	inv.Missing = missing(sig)
	switch {
	case len(inv.Hypotheses) == 0:
		inv.Summary = "No collected evidence points to a specific explanation yet. See the missing evidence below."
	default:
		top := inv.Hypotheses[0]
		inv.Summary = fmt.Sprintf("The evidence most supports: %s (%s evidence). This is a hypothesis to check, not a confirmed cause.",
			lowerFirst(top.Title), top.Strength)
	}
	return inv
}

func timeline(sig Signals) []TimelineEntry {
	var out []TimelineEntry
	for _, o := range sig.Subject {
		out = append(out, TimelineEntry{At: o.FirstSeen, Kind: "anomaly", RuleID: o.Finding.RuleID, Summary: o.Finding.Title})
	}
	for _, o := range sig.Others {
		out = append(out, TimelineEntry{At: o.FirstSeen, Kind: "finding", RuleID: o.Finding.RuleID, Summary: o.Finding.Title})
	}
	for _, g := range sig.Gaps {
		out = append(out, TimelineEntry{At: g.From, Kind: "gap",
			Summary: fmt.Sprintf("No %s data for %s", g.What, g.To.Sub(g.From).Round(time.Second))})
	}
	slices.SortStableFunc(out, func(a, b TimelineEntry) int { return a.At.Compare(b.At) })
	if out == nil {
		out = []TimelineEntry{}
	}
	return out
}

func missing(sig Signals) []string {
	var out []string
	if !sig.SamplingEnabled {
		out = append(out, "Active session sampling is off, so what the statement waited on is unknown.")
	} else if sig.StatementWaits == nil || sig.StatementWaits.Now == nil {
		out = append(out, "No active session samples of this statement in the period (it ran between samples, or pg_stat_activity.query_id is not available: compute_query_id).")
	}
	if !sig.PlanHistory {
		out = append(out, "No execution plan of this statement was saved, so a plan change cannot be checked. LockLens never runs EXPLAIN ANALYZE automatically.")
	}
	if len(sig.Gaps) > 0 {
		out = append(out, fmt.Sprintf("%d collection gap(s) in the period: events in them were not observed.", len(sig.Gaps)))
	}
	out = append(out, "Application deployments and configuration changes are not recorded by LockLens.")
	return out
}

func hasEvidence(f finding.Finding, label string) bool {
	for _, e := range f.Evidence {
		if e.Label == label {
			return true
		}
	}
	return false
}

func total(m map[string]float64) float64 {
	t := 0.0
	for _, v := range m {
		t += v
	}
	return t
}

func share(m map[string]float64, class string) float64 {
	t := total(m)
	if t <= 0 {
		return 0
	}
	return m[class] / t
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if r[0] >= 'A' && r[0] <= 'Z' {
		r[0] += 'a' - 'A'
	}
	return string(r)
}
