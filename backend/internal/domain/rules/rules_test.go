package rules

import (
	"strings"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/domain/collector"
	"github.com/locklens/locklens/internal/domain/model"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/domain/report"
)

var now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

func sess(pid int, state string, xactAge time.Duration, at time.Time) model.Session {
	start := now.Add(-2 * time.Hour) // constant: the same backend in every sample
	return model.Session{PID: pid, State: ptr(state), BackendType: ptr("client backend"), DetailsVisible: true,
		BackendStart: &start, XactStart: ptr(at.Add(-xactAge)), User: ptr("app"), ApplicationName: ptr("svc"),
		WaitingLocks: []model.WaitingLock{}}
}

// activityAt builds an activity collection with A (pid 1, given state)
// blocking B (2) blocking C (3).
func activityAt(at time.Time, rootState string, used int) (*observe.Activity, *report.Report) {
	snap := &model.Snapshot{SchemaVersion: 1, ServerTime: at, CollectedAt: at,
		Sessions: []model.Session{sess(1, rootState, 10*time.Minute, at), sess(2, "active", time.Minute, at), sess(3, "active", time.Minute, at)},
		Edges:    []model.Edge{{Blocker: 1, Waiter: 2}, {Blocker: 2, Waiter: 3}}}
	rep := report.Build(snap, 5*time.Minute)
	return &observe.Activity{At: at, Snapshot: snap, Counts: observe.ActivityCounts{UsedConnections: used, MaxConnections: 100, ReservedConnections: 3}}, rep
}

func inputWithHistory(samples int, every time.Duration, rootState string, used int) Input {
	var hist []ActivitySample
	var act *observe.Activity
	var rep *report.Report
	for i := samples - 1; i >= 0; i-- {
		at := now.Add(-time.Duration(i) * every)
		act, rep = activityAt(at, rootState, used)
		hist = append(hist, Summarize(act, rep))
	}
	return Input{Now: now, Interval: every, History: hist,
		View: collector.View{Activity: act, Report: rep, Capabilities: &observe.Capabilities{ReadAllStats: true, TrackActivities: true}}}
}

func find(fs []Finding, rule string) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.RuleID == rule {
			out = append(out, f)
		}
	}
	return out
}

func TestBlockingChainNeedsPersistence(t *testing.T) {
	th := Defaults()
	if got := find(blockingRules(inputWithHistory(2, 10*time.Second, "active", 10), th), RuleBlockingChain); len(got) != 0 {
		t.Fatalf("2 samples / 10 s must not fire (min 3 samples, 30 s): %+v", got)
	}
	got := find(blockingRules(inputWithHistory(4, 10*time.Second, "active", 10), th), RuleBlockingChain)
	if len(got) != 1 {
		t.Fatalf("4 samples over 30 s must fire once, got %d", len(got))
	}
	f := got[0]
	if !f.Trigger || f.Status != Active || f.RuleVersion != 1 || !strings.Contains(f.Title, "pid 1") {
		t.Errorf("finding = %+v", f)
	}
	if !strings.Contains(f.Limitations, "not a deadlock") {
		t.Error("limitations must say a chain is not a deadlock")
	}
	hasEv := func(label string) bool {
		for _, e := range f.Evidence {
			if e.Label == label {
				return true
			}
		}
		return false
	}
	for _, l := range []string{"root pid", "transitively blocked (unique)", "consecutive samples"} {
		if !hasEv(l) {
			t.Errorf("missing evidence %q", l)
		}
	}
}

func TestBlockingStreakBreaksOnGap(t *testing.T) {
	in := inputWithHistory(4, 10*time.Second, "active", 10)
	// Move the oldest two samples far into the past: a collection gap.
	in.History[0].At = now.Add(-30 * time.Minute)
	in.History[1].At = now.Add(-29 * time.Minute)
	if got := find(blockingRules(in, Defaults()), RuleBlockingChain); len(got) != 0 {
		t.Fatal("a streak must not span a collection gap")
	}
}

func TestIdleInTransactionBlocker(t *testing.T) {
	got := find(blockingRules(inputWithHistory(2, 10*time.Second, "idle in transaction", 10), Defaults()), RuleIdleBlocker)
	if len(got) != 1 || got[0].Severity != Critical || !got[0].Trigger {
		t.Fatalf("idle blocker = %+v", got)
	}
	if got := find(blockingRules(inputWithHistory(2, 10*time.Second, "active", 10), Defaults()), RuleIdleBlocker); len(got) != 0 {
		t.Fatal("an active root is not an idle-in-transaction blocker")
	}
}

func TestInsufficientDataNeverPasses(t *testing.T) {
	in := Input{Now: now, Interval: 10 * time.Second}
	fs := Evaluate(in, Defaults())
	seen := map[string]bool{}
	for _, f := range fs {
		if f.Status == InsufficientData {
			seen[f.RuleID] = true
		}
		if f.Status == Active {
			t.Errorf("no data must not produce active findings: %+v", f)
		}
	}
	for _, r := range []string{RuleBlockingChain, RuleIdleBlocker, RuleConnections, RuleLongTransaction, RuleMeanRegression,
		RuleCallVolume, RuleTempFiles, RuleDeadlocks, RuleDeadTuples, RuleXIDAge, RuleReplicaLag, RuleSlotRetainedWAL, RuleArchiverFailures} {
		if !seen[r] {
			t.Errorf("rule %s silently passed without data", r)
		}
	}
}

func TestStaleActivityIsInsufficient(t *testing.T) {
	in := inputWithHistory(4, 10*time.Second, "active", 10)
	in.Now = now.Add(10 * time.Minute)
	got := find(blockingRules(in, Defaults()), RuleBlockingChain)
	if len(got) != 1 || got[0].Status != InsufficientData {
		t.Fatalf("stale data must be insufficient, got %+v", got)
	}
}

func TestConnectionPressure(t *testing.T) {
	th := Defaults()
	if fs := connectionRule(inputWithHistory(3, 10*time.Second, "active", 70), th); len(fs) != 0 {
		t.Fatalf("72%% must not fire: %+v", fs)
	}
	fs := connectionRule(inputWithHistory(3, 10*time.Second, "active", 95), th)
	if len(fs) != 1 || fs[0].Severity != Critical {
		t.Fatalf("95/97 used must be critical: %+v", fs)
	}
	fs = connectionRule(inputWithHistory(1, 10*time.Second, "active", 95), th)
	if len(fs) != 0 {
		t.Fatal("a single sample is below connections_min_samples")
	}
}

func TestLongTransaction(t *testing.T) {
	fs := longTransactionRule(inputWithHistory(1, 10*time.Second, "idle in transaction", 10), Defaults())
	if len(fs) != 1 || !strings.Contains(fs[0].Title, "pid 1") || fs[0].Severity != Warning {
		t.Fatalf("long tx = %+v", fs)
	}
}

func statements(perWindow func(window int) (calls int64, ms float64), window time.Duration, step time.Duration) []observe.StatementsCollection {
	var out []observe.StatementsCollection
	key := observe.QueryKey{ServerMajor: 17, DBID: 1, UserID: 2, TopLevel: true, QueryID: 42}
	n := int(2 * window / step)
	for i := n - 1; i >= 0; i-- {
		at := now.Add(-time.Duration(i) * step)
		w := 0
		if at.After(now.Add(-window)) {
			w = 1
		}
		calls, ms := perWindow(w)
		per := int(window / step)
		iv := step.Seconds()
		out = append(out, observe.StatementsCollection{At: at, Interval: &iv,
			Deltas: []observe.QueryDelta{{Key: key, DatName: "app", UserName: "u", Calls: calls / int64(per), TotalMS: ms / float64(per)}}})
	}
	return out
}

func workloadInput(colls []observe.StatementsCollection) Input {
	return Input{Now: now, Interval: 10 * time.Second, View: collector.View{
		Capabilities: &observe.Capabilities{StatStatements: true}, Statements: colls}}
}

func TestMeanRegressionAndVolume(t *testing.T) {
	th := Defaults()
	win := 15 * time.Minute
	// 1000 calls per window; mean 2 ms before, 10 ms after.
	slow := statements(func(w int) (int64, float64) {
		if w == 0 {
			return 1500, 3000
		}
		return 1500, 15000
	}, win, time.Minute)
	fs := workloadRules(workloadInput(slow), th)
	if r := find(fs, RuleMeanRegression); len(r) != 1 || !r[0].Trigger {
		t.Fatalf("mean regression = %+v", fs)
	}
	if v := find(fs, RuleCallVolume); len(v) != 0 {
		t.Fatal("same call count must not be a volume increase")
	}
	more := statements(func(w int) (int64, float64) {
		if w == 0 {
			return 600, 1200
		}
		return 3000, 6000
	}, win, time.Minute)
	fs = workloadRules(workloadInput(more), th)
	if v := find(fs, RuleCallVolume); len(v) != 1 {
		t.Fatalf("volume increase = %+v", fs)
	}
	if r := find(fs, RuleMeanRegression); len(r) != 0 {
		t.Fatal("constant mean must not be a regression")
	}
}

func TestWorkloadTooFewCallsAndCoverage(t *testing.T) {
	th := Defaults()
	few := statements(func(w int) (int64, float64) {
		if w == 0 {
			return 15, 15
		}
		return 15, 1500
	}, 15*time.Minute, time.Minute)
	if fs := workloadRules(workloadInput(few), th); len(find(fs, RuleMeanRegression)) != 0 {
		t.Fatal("below regression_min_calls nothing may fire")
	}
	short := statements(func(int) (int64, float64) { return 1500, 3000 }, 15*time.Minute, time.Minute)[20:]
	fs := workloadRules(workloadInput(short), th)
	if r := find(fs, RuleMeanRegression); len(r) != 1 || r[0].Status != InsufficientData {
		t.Fatalf("low coverage must be insufficient: %+v", fs)
	}
	c := Compare(WindowAgg{Calls: 100, TotalMS: 100}, WindowAgg{Calls: 100, TotalMS: 1000}, th)
	if !c.MeanRegression || !c.LoadIncrease || c.VolumeIncrease || *c.MeanRatio != 10 {
		t.Errorf("compare = %+v", c)
	}
}

func series(values []float64, step time.Duration) []collector.TimedValue {
	var out []collector.TimedValue
	for i, v := range values {
		at := now.Add(-time.Duration(len(values)-1-i) * step)
		vv := v
		out = append(out, collector.TimedValue{At: at, Value: &vv})
	}
	return out
}

func TestTempDeadlocksArchiver(t *testing.T) {
	th := Defaults()
	vals := make([]float64, 61)
	for i := range vals {
		if i > 30 {
			vals[i] = 1 << 20 // 1 MiB/s in the current window
		} else {
			vals[i] = 1 << 10
		}
	}
	in := Input{Now: now, View: collector.View{Points: map[string][]collector.TimedValue{
		"temp.bytes.per_s":      series(vals, 30*time.Second),
		"deadlocks.delta":       series(append(make([]float64, 59), 0, 2), 30*time.Second),
		"archiver.failed.delta": series(make([]float64, 61), 30*time.Second),
	}}}
	if fs := tempFilesRule(in, th); len(fs) != 1 || fs[0].Status != Active {
		t.Fatalf("temp = %+v", fs)
	}
	if fs := deadlocksRule(in, th); len(fs) != 1 || !strings.Contains(fs[0].Title, "2 deadlock") {
		t.Fatalf("deadlocks = %+v", fs)
	}
	if fs := archiverRule(in, th); len(fs) != 0 {
		t.Fatal("no archiver failures expected")
	}
	// Unknown values break coverage.
	unknown := series(vals, 30*time.Second)
	for i := range unknown {
		unknown[i].Value = nil
	}
	in.View.Points["temp.bytes.per_s"] = unknown
	if fs := tempFilesRule(in, th); len(fs) != 1 || fs[0].Status != InsufficientData {
		t.Fatalf("unknown temp rates must be insufficient: %+v", fs)
	}
}

func TestVacuumRules(t *testing.T) {
	th := Defaults()
	old := now.Add(-48 * time.Hour)
	recent := now.Add(-time.Hour)
	in := Input{Now: now, View: collector.View{Tables: &observe.TableHealth{At: now,
		Settings: map[string]string{"autovacuum_freeze_max_age": "200000000"},
		Tables: []observe.TableStat{
			{RelID: 1, Schema: "public", Name: "bloated", DeadTuples: ptr(int64(50000)), LiveTuples: ptr(int64(50000)), LastAutovacuum: &old},
			{RelID: 2, Schema: "public", Name: "vacuumed", DeadTuples: ptr(int64(50000)), LiveTuples: ptr(int64(50000)), LastAutovacuum: &recent},
			{RelID: 3, Schema: "public", Name: "clean", DeadTuples: ptr(int64(10)), LiveTuples: ptr(int64(50000))},
		},
		Databases: []observe.DatabaseAge{{DatName: "app", XIDAge: ptr(int64(190000000))}, {DatName: "ok", XIDAge: ptr(int64(1000))}},
	}}}
	fs := vacuumRules(in, th)
	dead := find(fs, RuleDeadTuples)
	if len(dead) != 1 || !strings.Contains(dead[0].Title, "public.bloated") || !strings.Contains(dead[0].Limitations, "estimates") {
		t.Fatalf("dead tuples = %+v", dead)
	}
	xid := find(fs, RuleXIDAge)
	if len(xid) != 1 || xid[0].Severity != Critical {
		t.Fatalf("xid age = %+v", xid)
	}
}

func TestReplicationRules(t *testing.T) {
	th := Defaults()
	lag := float64(200 << 20)
	retained := float64(2 << 30)
	in := Input{Now: now, View: collector.View{
		Replication: &observe.Replication{At: now, Role: "primary",
			Replicas: []observe.Replica{{PID: 9, ApplicationName: "standby1", ReplayLagBytes: &lag}},
			Slots:    []observe.Slot{{Name: "stale", Type: "physical", Active: false, RetainedBytes: &retained}, {Name: "fine", RetainedBytes: ptr(float64(1024))}}},
		Points: map[string][]collector.TimedValue{"replication.replay_lag_bytes{standby1}": series([]float64{lag, lag, lag}, 30*time.Second)},
	}}
	fs := replicationRules(in, th)
	if r := find(fs, RuleReplicaLag); len(r) != 1 {
		t.Fatalf("replica lag = %+v", fs)
	}
	s := find(fs, RuleSlotRetainedWAL)
	if len(s) != 1 || !s[0].Trigger || !strings.Contains(s[0].Title, "stale") {
		t.Fatalf("slots = %+v", s)
	}
}

func TestThresholdValidation(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
	bad := Defaults()
	bad.ConnectionsWarnRatio = 1.5
	bad.RegressionMeanRatio = 0.5
	bad.BlockingMinSamples = 0
	err := bad.Validate()
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"connections_warn_ratio", "regression_mean_ratio", "blocking_min_samples"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %s: %v", want, err)
		}
	}
}

func TestPIDReuseStartsNewStreak(t *testing.T) {
	in := inputWithHistory(4, 10*time.Second, "active", 10)
	// Same PID, different backend: a different session.
	for i := 0; i < 3; i++ {
		other := now.Add(-time.Duration(10+i) * time.Hour)
		for j := range in.History[i].Roots {
			in.History[i].Roots[j].Key = rootKey(1, &other)
		}
	}
	if got := find(blockingRules(in, Defaults()), RuleBlockingChain); len(got) != 0 {
		t.Fatal("a reused PID must not continue another session's streak")
	}
}
