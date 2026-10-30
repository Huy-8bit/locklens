package rules

import (
	"strings"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/domain/baseline"
	"github.com/locklens/locklens/internal/domain/finding"
	"github.com/locklens/locklens/internal/domain/live"
	"github.com/locklens/locklens/internal/domain/model"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/domain/report"
)

var key42 = observe.QueryKey{ServerMajor: 17, DBID: 1, UserID: 2, TopLevel: true, QueryID: 42}

// perMinute builds one-minute collections over two workload windows; prev and
// cur are the deltas of one collection in the previous and current window.
func perMinute(win time.Duration, prev, cur observe.QueryDelta) []observe.StatementsCollection {
	var out []observe.StatementsCollection
	n := int(2 * win / time.Minute)
	for i := n - 1; i >= 0; i-- {
		at := now.Add(-time.Duration(i) * time.Minute)
		d := prev
		if at.After(now.Add(-win)) {
			d = cur
		}
		d.Key, d.DatName, d.UserName = key42, "app", "u"
		iv := 60.0
		out = append(out, observe.StatementsCollection{At: at, Interval: &iv, Deltas: []observe.QueryDelta{d}})
	}
	return out
}

// hourly builds a 7-day baseline of hourly points ending an hour before now;
// point gives each hour's activity.
func hourly(point func(h int, i int) baseline.Point) baseline.Baseline {
	to := now.Add(-time.Hour).Truncate(time.Hour)
	from := to.Add(-7 * 24 * time.Hour)
	cov := map[time.Time]float64{}
	var pts []baseline.Point
	i := 0
	for at := from; at.Before(to); at = at.Add(time.Hour) {
		cov[at] = 3600
		p := point(at.Hour(), i)
		p.At = at
		pts = append(pts, p)
		i++
	}
	return baseline.Build(pts, cov, nil, from, to, 3600, 20)
}

// steady: 1 call per second at about 2 ms, 1 row and 10 reads per call, with
// some natural variation.
func steady(_ int, i int) baseline.Point {
	jitter := float64(i%5) * 0.05 // 0 .. 0.2 ms
	return baseline.Point{Calls: 3600, TotalMS: 3600 * (1.9 + jitter), Rows: 3600, BlkRead: 36000, BlkHit: 360000}
}

func anomalyInput(colls []observe.StatementsCollection, b map[observe.QueryKey]baseline.Baseline) Input {
	in := workloadInput(colls)
	in.Baselines = b
	return in
}

func TestLatencyRegressionAgainstBaseline(t *testing.T) {
	th := Defaults()
	// Both windows are slow (10 ms): the previous window alone would see no
	// change, the baseline does.
	slow := observe.QueryDelta{Calls: 100, TotalMS: 1000, Rows: 100, BlkRead: 1000}
	in := anomalyInput(perMinute(15*time.Minute, slow, slow), map[observe.QueryKey]baseline.Baseline{key42: hourly(steady)})
	fs := workloadRules(in, th)
	r := find(fs, RuleMeanRegression)
	if len(r) != 1 || !r[0].Trigger {
		t.Fatalf("regression = %+v", fs)
	}
	f := r[0]
	if f.Severity != Critical { // 10 ms against ~2 ms is beyond regression_critical_ratio (5)
		t.Errorf("severity = %v, ratio %v", f.Severity, f.Metrics["ratio"])
	}
	if f.Metrics["baseline_mean_ms"] < 1.9 || f.Metrics["baseline_mean_ms"] > 2.1 || f.Metrics["z"] < th.AnomalySensitivity {
		t.Errorf("metrics = %+v", f.Metrics)
	}
	if f.Confidence == nil || f.Confidence.Level != finding.ConfidenceHigh {
		t.Errorf("confidence = %+v", f.Confidence)
	}
	if evidence(f, "compared with") != "baseline" {
		t.Errorf("evidence = %+v", f.Evidence)
	}
	// Same windows without a baseline: nothing changed between them.
	if r := find(workloadRules(anomalyInput(perMinute(15*time.Minute, slow, slow), nil), th), RuleMeanRegression); len(r) != 0 {
		t.Fatalf("without a baseline equal windows must not regress: %+v", r)
	}
}

func evidence(f Finding, label string) any {
	for _, e := range f.Evidence {
		if e.Label == label {
			return e.Value
		}
	}
	return nil
}

func TestBaselineFollowsTheHourOfDay(t *testing.T) {
	th := Defaults()
	// At noon this statement is always 10 ms (a daily report); elsewhere 2 ms.
	b := hourly(func(h, i int) baseline.Point {
		p := steady(h, i)
		if h == 11 || h == 12 {
			p.TotalMS *= 5
		}
		return p
	})
	slow := observe.QueryDelta{Calls: 100, TotalMS: 1000, Rows: 100, BlkRead: 1000}
	fs := workloadRules(anomalyInput(perMinute(15*time.Minute, slow, slow), map[observe.QueryKey]baseline.Baseline{key42: b}), th)
	if r := find(fs, RuleMeanRegression); len(r) != 0 {
		t.Fatalf("usual for this hour, yet reported: %+v", r[0].Observed)
	}
}

func TestWarmupFallsBackToThePreviousWindow(t *testing.T) {
	th := Defaults()
	b := hourly(steady)
	b.All.Points, b.ByHour = 3, nil // not warm
	fast := observe.QueryDelta{Calls: 100, TotalMS: 200, Rows: 100}
	slow := observe.QueryDelta{Calls: 100, TotalMS: 1000, Rows: 100}
	fs := workloadRules(anomalyInput(perMinute(15*time.Minute, fast, slow), map[observe.QueryKey]baseline.Baseline{key42: b}), th)
	r := find(fs, RuleMeanRegression)
	if len(r) != 1 || evidence(r[0], "compared with") != "previous window" {
		t.Fatalf("fallback = %+v", fs)
	}
	if r[0].Confidence == nil || r[0].Confidence.Level == finding.ConfidenceHigh {
		t.Errorf("a previous-window comparison is not high confidence: %+v", r[0].Confidence)
	}
}

func TestTooFewCallsAreNotAnomalies(t *testing.T) {
	th := Defaults()
	// 2 calls a minute (30 per window) at 50 ms: below regression_min_calls.
	slow := observe.QueryDelta{Calls: 2, TotalMS: 100, Rows: 2}
	fs := workloadRules(anomalyInput(perMinute(15*time.Minute, slow, slow), map[observe.QueryKey]baseline.Baseline{key42: hourly(steady)}), th)
	for _, f := range fs {
		if f.Status == Active {
			t.Fatalf("unexpected finding %s: %s", f.RuleID, f.Title)
		}
	}
}

func TestVolumeStormLoadAndIO(t *testing.T) {
	th := Defaults()
	// 30 calls/s instead of 1, same speed: a storm and a load increase.
	storm := observe.QueryDelta{Calls: 1800, TotalMS: 1800 * 2, Rows: 1800, BlkRead: 18000}
	fs := workloadRules(anomalyInput(perMinute(15*time.Minute, storm, storm), map[observe.QueryKey]baseline.Baseline{key42: hourly(steady)}), th)
	v := find(fs, RuleCallVolume)
	if len(v) != 1 || v[0].Severity != Warning || !strings.HasPrefix(v[0].Title, "Query storm") {
		t.Fatalf("volume = %+v", v)
	}
	if l := find(fs, RuleLoadIncrease); len(l) != 1 || l[0].Metrics["added_load_ms_per_s"] < 50 {
		t.Fatalf("load = %+v", fs)
	}
	if r := find(fs, RuleMeanRegression); len(r) != 0 {
		t.Fatal("same time per call is not a latency regression")
	}
	// 500 blocks read per call instead of 10.
	io := observe.QueryDelta{Calls: 60, TotalMS: 120, Rows: 60, BlkRead: 30000}
	fs = workloadRules(anomalyInput(perMinute(15*time.Minute, io, io), map[observe.QueryKey]baseline.Baseline{key42: hourly(steady)}), th)
	if r := find(fs, RuleIORegression); len(r) != 1 || r[0].Metrics["ratio"] < 40 {
		t.Fatalf("io = %+v", fs)
	}
	// 50 rows per call instead of 1.
	rows := observe.QueryDelta{Calls: 60, TotalMS: 120, Rows: 3000, BlkRead: 600}
	fs = workloadRules(anomalyInput(perMinute(15*time.Minute, rows, rows), map[observe.QueryKey]baseline.Baseline{key42: hourly(steady)}), th)
	if r := find(fs, RulePatternChange); len(r) != 1 {
		t.Fatalf("pattern = %+v", fs)
	}
}

func TestPossibleNPlusOne(t *testing.T) {
	th := Defaults()
	text := "SELECT * FROM items WHERE id = $1"
	lookup := observe.QueryDelta{Calls: 6000, TotalMS: 1200, Rows: 6000, Command: observe.CommandSelect, Text: &text}
	fs := workloadRules(anomalyInput(perMinute(15*time.Minute, lookup, lookup), nil), th)
	r := find(fs, RuleNPlusOne)
	if len(r) != 1 || r[0].Severity != Info || r[0].Trigger || r[0].Confidence.Level != finding.ConfidenceLow {
		t.Fatalf("n+1 = %+v", fs)
	}
	if !strings.Contains(r[0].Explanation, "cannot prove") {
		t.Errorf("an N+1 finding must say it is not proven: %s", r[0].Explanation)
	}
	constant := "SELECT now()"
	lookup.Text = &constant
	if r := find(workloadRules(anomalyInput(perMinute(15*time.Minute, lookup, lookup), nil), th), RuleNPlusOne); len(r) != 0 {
		t.Fatal("a statement without parameters is not a per-item lookup")
	}
}

// runningInput is one activity snapshot with pid 7 running a statement for age.
func runningInput(age time.Duration, queryID *int64, mutate func(*model.Session)) Input {
	start := now.Add(-time.Hour)
	s := model.Session{PID: 7, State: ptr("active"), BackendType: ptr("client backend"), DetailsVisible: true, BackendStart: &start,
		XactStart: ptr(now.Add(-age)), QueryStart: ptr(now.Add(-age)), QueryID: queryID, User: ptr("app"), Database: ptr("app"),
		WaitingLocks: []model.WaitingLock{}}
	if mutate != nil {
		mutate(&s)
	}
	snap := &model.Snapshot{SchemaVersion: 1, ServerTime: now, CollectedAt: now, Sessions: []model.Session{s}}
	rep := report.Build(snap, 5*time.Minute)
	act := &observe.Activity{At: now, Snapshot: snap, Counts: observe.ActivityCounts{UsedConnections: 1, MaxConnections: 100}}
	return Input{Now: now, Interval: 10 * time.Second, History: []ActivitySample{Summarize(act, rep)},
		View: live.View{Activity: act, Report: rep, Capabilities: &observe.Capabilities{ReadAllStats: true, TrackActivities: true}}}
}

func TestLongRunningQuery(t *testing.T) {
	th := Defaults()
	fs := longRunningRule(runningInput(10*time.Minute, nil, nil), th)
	if len(fs) != 1 || fs[0].Severity != Critical || !fs[0].Trigger || !strings.HasPrefix(fs[0].Key, "pid:7:") {
		t.Fatalf("critical = %+v", fs)
	}
	if fs := longRunningRule(runningInput(time.Minute, nil, nil), th); len(fs) != 1 || fs[0].Severity != Warning || fs[0].Trigger {
		t.Fatalf("warning = %+v", fs)
	}
	if fs := longRunningRule(runningInput(20*time.Second, nil, nil), th); len(fs) != 0 {
		t.Fatalf("below thresholds without a baseline: %+v", fs)
	}
	// 20 s for a statement that usually takes 2 ms is far beyond its usual time.
	in := runningInput(20*time.Second, ptr(int64(42)), nil)
	in.Baselines = map[observe.QueryKey]baseline.Baseline{key42: hourly(steady)}
	fs = longRunningRule(in, th)
	if len(fs) != 1 || fs[0].Severity != Warning || fs[0].Metrics["expected_ms"] < 1.9 {
		t.Fatalf("expected-based = %+v", fs)
	}
	// Idle sessions and parallel workers are not running statements.
	if fs := longRunningRule(runningInput(10*time.Minute, nil, func(s *model.Session) { s.State = ptr("idle") }), th); len(fs) != 0 {
		t.Fatalf("idle = %+v", fs)
	}
	if fs := longRunningRule(runningInput(10*time.Minute, nil, func(s *model.Session) { s.LeaderPID = ptr(3) }), th); len(fs) != 0 {
		t.Fatalf("worker = %+v", fs)
	}
	// A new execution in the same backend is a new finding.
	a := longRunningRule(runningInput(10*time.Minute, nil, nil), th)[0].Key
	b := longRunningRule(runningInput(11*time.Minute, nil, nil), th)[0].Key
	if a == b {
		t.Fatal("each execution needs its own key")
	}
}
