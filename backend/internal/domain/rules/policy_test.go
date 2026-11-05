package rules

import (
	"strings"
	"testing"
	"time"
)

func TestEffectivePolicyLayers(t *testing.T) {
	env := &Override{Modes: map[string]string{RuleDeadlocks: ModeIncident}, Thresholds: map[string]float64{"blocking_min_seconds": 60}}
	tgt := &Override{Modes: map[string]string{RuleDeadlocks: ModeOff}, Thresholds: map[string]float64{"connections_warn_ratio": 0.7}}
	p, err := Effective(Defaults(), map[string]string{RuleTempFiles: ModeIncident}, env, tgt)
	if err != nil {
		t.Fatal(err)
	}
	if p.Thresholds.BlockingMinSeconds != 60 || p.Thresholds.ConnectionsWarnRatio != 0.7 || p.Thresholds.LongTxWarnSeconds != Defaults().LongTxWarnSeconds {
		t.Fatalf("thresholds %+v", p.Thresholds)
	}
	if p.Mode(RuleDeadlocks) != ModeOff || p.Mode(RuleTempFiles) != ModeIncident || p.Mode(RuleTargetUnreachable) != ModeIncident || p.Mode(RuleDeadTuples) != ModeFinding {
		t.Fatalf("modes %+v", p.Modes)
	}
	if p.Sources["blocking_min_seconds"] != "environment" || p.Sources["connections_warn_ratio"] != "instance" || p.Sources["mode:"+RuleDeadlocks] != "instance" {
		t.Fatalf("sources %+v", p.Sources)
	}
	// Overrides that make thresholds invalid, or name unknown things, fail.
	bad := Overrides{Environments: map[string]Override{"prod": {Thresholds: map[string]float64{"connections_warn_ratio": 2}}},
		Targets: map[int64]Override{3: {Modes: map[string]string{"nope": "incident", RuleXIDAge: "loud"}}}}
	err = bad.Validate(Defaults())
	if err == nil || !strings.Contains(err.Error(), "environment prod") || !strings.Contains(err.Error(), "unknown rule") || !strings.Contains(err.Error(), "mode must be") {
		t.Fatalf("validate: %v", err)
	}
	if err := (Override{Thresholds: map[string]float64{"no_such": 1}}).Validate(); err == nil {
		t.Fatal("unknown threshold accepted")
	}
}

func TestFiresByMode(t *testing.T) {
	p, _ := Effective(Defaults(), nil, nil, nil)
	lag := Finding{RuleID: RuleReplicaLag, Status: Active, Severity: Warning}
	if !p.Fires(lag) {
		t.Fatal("replica lag opens an incident by default")
	}
	if p.Fires(Finding{RuleID: RuleDeadTuples, Status: Active, Severity: Warning}) {
		t.Fatal("dead tuples report findings only by default")
	}
	// Rules with their own trigger decide themselves.
	if p.Fires(Finding{RuleID: RuleBlockingChain, Status: Active, Severity: Critical}) {
		t.Fatal("blocking needs Trigger")
	}
	if p.Fires(Finding{RuleID: RuleReplicaLag, Status: Active, Severity: Info}) {
		t.Fatal("info never fires")
	}
	off, _ := Effective(Defaults(), map[string]string{RuleReplicaLag: ModeOff}, nil, nil)
	if off.Fires(lag) || len(off.Filter([]Finding{lag})) != 0 {
		t.Fatal("an off rule neither fires nor reports")
	}
}

func TestUnreachableAfterTargetDownSeconds(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	th := Defaults()
	r := Reachability{FailingSince: now.Add(-30 * time.Second), Attempts: 3, Error: "connection refused"}
	if fs := Unreachable(r, now, th); len(fs) != 0 {
		t.Fatalf("too early: %+v", fs)
	}
	r.FailingSince = now.Add(-90 * time.Second)
	fs := Unreachable(r, now, th)
	if len(fs) != 1 || fs[0].RuleID != RuleTargetUnreachable || fs[0].Severity != Critical || fs[0].Title != "Instance cannot be reached" || !strings.Contains(fs[0].Observed[0], "for 1m30s") {
		t.Fatalf("unreachable %+v", fs)
	}
	r.Attempts = 1
	if fs := Unreachable(r, now, th); len(fs) != 0 {
		t.Fatal("one failed attempt is not enough")
	}
}

func TestFmtDuration(t *testing.T) {
	for sec, want := range map[float64]string{45: "45s", 90: "1m30s", 120: "2m", 3600: "1h0m", 3630: "1h0m30s"} {
		if got := fmtDuration(sec); got != want {
			t.Errorf("%v: %q, want %q", sec, got, want)
		}
	}
}
