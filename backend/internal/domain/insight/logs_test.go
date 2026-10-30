package insight

import (
	"strings"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/domain/finding"
	"github.com/locklens/locklens/internal/domain/logs"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/domain/rules"
)

func TestServerLogEvidence(t *testing.T) {
	since := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	qid, other := int64(42), int64(7)
	ms := 1500.0
	f := finding.Finding{RuleID: rules.RuleMeanRegression, Key: "q:1", Metrics: map[string]float64{}}
	sig := Signals{At: since.Add(10 * time.Minute), Since: since, Subject: []Observed{{Finding: f, FirstSeen: since}},
		QueryID: &qid, LogsShipped: true, Logs: []observe.LogEvent{
			{Kind: logs.KindLockWait, QueryID: &qid, DurationMS: &ms},
			{Kind: logs.KindLockWait, QueryID: &other},
			{Kind: logs.KindTempFile, QueryID: &qid, Details: map[string]any{"bytes": float64(200 << 20)}},
		}}
	inv := Correlate(sig)
	var lock, plan Hypothesis
	for _, h := range inv.Hypotheses {
		switch h.ID {
		case "lock_contention":
			lock = h
		case "plan_change":
			plan = h
		}
	}
	if len(lock.Supporting) != 2 || !strings.Contains(lock.Supporting[0], "1500 ms") {
		t.Fatalf("lock %+v", lock)
	}
	if len(plan.Supporting) != 1 || !strings.Contains(plan.Supporting[0], "200.0 MB") {
		t.Fatalf("plan %+v", plan)
	}
	for _, m := range inv.Missing {
		if strings.Contains(m, "locklens agent") {
			t.Fatal("logs are shipped")
		}
	}
}
