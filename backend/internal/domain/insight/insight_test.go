package insight

import (
	"strings"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/domain/finding"
	"github.com/locklens/locklens/internal/domain/rules"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func TestImpactIsExplainedAndBounded(t *testing.T) {
	f := finding.Finding{Severity: finding.Critical, Metrics: map[string]float64{"share_of_load": 0.5, "added_load_ms_per_s": 999},
		Confidence: &finding.Confidence{Level: finding.ConfidenceHigh}}
	im := Score(f, 30*time.Minute)
	sum := 0.0
	for _, c := range im.Components {
		if c.Points < 0 || c.Points > c.Max || c.Detail == "" {
			t.Errorf("component %+v", c)
		}
		sum += c.Points
	}
	if im.Score != float64(int(sum*10+0.5))/10 || im.Score < 50 || im.Score > 100 {
		t.Fatalf("score %v sum %v", im.Score, sum)
	}
	// Less confidence, less impact; less load, less impact.
	low := f
	low.Confidence = &finding.Confidence{Level: finding.ConfidenceLow}
	if Score(low, 30*time.Minute).Score >= im.Score {
		t.Error("low confidence must lower the score")
	}
	small := f
	small.Metrics = map[string]float64{"share_of_load": 0.01, "added_load_ms_per_s": 1}
	if Score(small, 30*time.Minute).Score >= im.Score {
		t.Error("a small share must lower the score")
	}
	// Missing measurements add nothing rather than counting as zero load.
	if im := Score(finding.Finding{Severity: finding.Info}, 0); len(im.Components) != 1 || im.Score != 3.2 {
		t.Errorf("bare finding = %+v", im)
	}
}

func anomaly(rule string, metrics map[string]float64) Observed {
	return Observed{FirstSeen: t0, Finding: finding.Finding{RuleID: rule, Title: rule, Metrics: metrics}}
}

func TestCorrelateLockContention(t *testing.T) {
	sig := Signals{At: t0.Add(10 * time.Minute), Since: t0, SamplingEnabled: true,
		Subject: []Observed{anomaly(rules.RuleMeanRegression, map[string]float64{"mean_ms": 50, "baseline_mean_ms": 2, "rows_per_call": 1, "baseline_rows_per_call": 1,
			"calls_per_s": 1, "baseline_calls_per_s": 1})},
		Others:         []Observed{{FirstSeen: t0.Add(-2 * time.Minute), Finding: finding.Finding{RuleID: rules.RuleBlockingChain, Title: "Blocking chain rooted at pid 9"}}},
		StatementWaits: &WaitShift{Now: map[string]float64{"Lock": 3, "CPU": 1}, Before: map[string]float64{"CPU": 1}}}
	inv := Correlate(sig)
	if len(inv.Hypotheses) == 0 || inv.Hypotheses[0].ID != "lock_contention" || inv.Hypotheses[0].Strength != StrengthStrong {
		t.Fatalf("hypotheses = %+v", inv.Hypotheses)
	}
	if !strings.Contains(strings.Join(inv.Hypotheses[0].Supporting, " "), "before the anomaly") {
		t.Errorf("temporal order missing: %+v", inv.Hypotheses[0].Supporting)
	}
	for _, h := range inv.Hypotheses {
		if h.ID == "traffic_increase" {
			t.Error("unchanged calls must not support a traffic hypothesis")
		}
	}
	if !strings.Contains(inv.Summary, "not a confirmed cause") || inv.Disclaimer == "" {
		t.Errorf("summary = %q", inv.Summary)
	}
	if len(inv.Timeline) != 2 || inv.Timeline[0].Kind != "finding" {
		t.Errorf("timeline = %+v", inv.Timeline)
	}
	// Plan history is missing evidence; sampling worked.
	if m := strings.Join(inv.Missing, " "); !strings.Contains(m, "plan") || strings.Contains(m, "sampling is off") {
		t.Errorf("missing = %+v", inv.Missing)
	}
}

func TestCorrelatePlanChangeVersusTraffic(t *testing.T) {
	plan := Correlate(Signals{At: t0, Since: t0, Subject: []Observed{anomaly(rules.RuleMeanRegression, map[string]float64{
		"mean_ms": 40, "baseline_mean_ms": 4, "rows_per_call": 10, "baseline_rows_per_call": 10, "reads_per_call": 5000, "baseline_reads_per_call": 50,
		"calls_per_s": 2, "baseline_calls_per_s": 2})}})
	if plan.Hypotheses[0].ID != "plan_change" {
		t.Fatalf("plan = %+v", plan.Hypotheses)
	}
	if !strings.Contains(strings.Join(plan.Missing, " "), "sampling is off") {
		t.Error("sampling off must be reported as missing evidence")
	}
	traffic := Correlate(Signals{At: t0, Since: t0, Subject: []Observed{anomaly(rules.RuleLoadIncrease, map[string]float64{
		"mean_ms": 2, "baseline_mean_ms": 2, "calls_per_s": 80, "baseline_calls_per_s": 5, "rows_per_call": 1, "baseline_rows_per_call": 1})}})
	if traffic.Hypotheses[0].ID != "traffic_increase" {
		t.Fatalf("traffic = %+v", traffic.Hypotheses)
	}
	// Deterministic: the same signals give the same result.
	again := Correlate(Signals{At: t0, Since: t0, Subject: []Observed{anomaly(rules.RuleLoadIncrease, map[string]float64{
		"mean_ms": 2, "baseline_mean_ms": 2, "calls_per_s": 80, "baseline_calls_per_s": 5, "rows_per_call": 1, "baseline_rows_per_call": 1})}})
	if again.Summary != traffic.Summary || len(again.Hypotheses) != len(traffic.Hypotheses) {
		t.Error("not deterministic")
	}
}

func TestCorrelateWithoutEvidence(t *testing.T) {
	inv := Correlate(Signals{At: t0, Since: t0, Subject: []Observed{anomaly(rules.RuleMeanRegression, nil)}})
	if len(inv.Hypotheses) != 0 || !strings.Contains(inv.Summary, "No collected evidence") {
		t.Fatalf("inv = %+v", inv)
	}
}

func TestCorrelateSharedPressure(t *testing.T) {
	inv := Correlate(Signals{At: t0, Since: t0, SamplingEnabled: true, OtherRegressions: 4,
		Subject:       []Observed{anomaly(rules.RuleMeanRegression, map[string]float64{"mean_ms": 10, "baseline_mean_ms": 2})},
		InstanceWaits: &WaitShift{Now: map[string]float64{"CPU": 12}, Before: map[string]float64{"CPU": 3}}})
	if inv.Hypotheses[0].ID != "resource_saturation" {
		t.Fatalf("hypotheses = %+v", inv.Hypotheses)
	}
}

func TestStatementKey(t *testing.T) {
	k, ok := StatementKey("q:17:5:10:true:-9193564501488909311")
	if !ok || k.ServerMajor != 17 || k.DBID != 5 || k.UserID != 10 || !k.TopLevel || k.QueryID != -9193564501488909311 {
		t.Fatalf("key = %+v %v", k, ok)
	}
	for _, bad := range []string{"pid:1:2:3", "q:17:5:10:true", "q:x:5:10:true:1", "q:17:5:10:maybe:1"} {
		if _, ok := StatementKey(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}
