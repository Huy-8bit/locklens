package insight

import (
	"testing"
	"time"

	"github.com/locklens/locklens/internal/domain/changes"
	"github.com/locklens/locklens/internal/domain/finding"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/domain/rules"
)

func TestChangesBeforeAnAnomaly(t *testing.T) {
	since := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	f := finding.Finding{RuleID: rules.RuleMeanRegression, Key: "q:1", Metrics: map[string]float64{}}
	sig := Signals{At: since.Add(10 * time.Minute), Since: since, Subject: []Observed{{Finding: f, FirstSeen: since}},
		Changes: []observe.ChangeEvent{
			{At: since.Add(-5 * time.Minute), Kind: changes.KindDeploy, Title: "api v2.3.1"},
			{At: since.Add(-20 * time.Minute), Kind: changes.KindSchema, Title: "app: index public.orders_customer dropped",
				Details: map[string]any{"changes": []any{map[string]any{"object": "index", "name": "public.orders_customer", "change": "dropped"}}}},
			{At: since.Add(-10 * time.Minute), Kind: changes.KindSettings, Title: "Setting work_mem changed",
				Details: map[string]any{"changes": []changes.SettingChange{{Name: "work_mem"}}}},
			// After the anomaly began: not a lead.
			{At: since.Add(5 * time.Minute), Kind: changes.KindDeploy, Title: "api v2.3.2"},
		}}
	inv := Correlate(sig)
	byID := map[string]Hypothesis{}
	for _, h := range inv.Hypotheses {
		byID[h.ID] = h
	}
	recent, ok := byID["recent_change"]
	if !ok || len(recent.Supporting) != 1 {
		t.Fatalf("a deploy just before is a hypothesis: %+v", inv.Hypotheses)
	}
	plan := byID["plan_change"]
	if len(plan.Supporting) != 2 {
		t.Fatalf("a dropped index and a planner setting support a plan change: %+v", plan)
	}
	n := 0
	for _, e := range inv.Timeline {
		if e.Kind == "change" {
			n++
		}
	}
	if n != 4 {
		t.Fatalf("every change is on the timeline: %+v", inv.Timeline)
	}
}
