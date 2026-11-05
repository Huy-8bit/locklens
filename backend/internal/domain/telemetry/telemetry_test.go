package telemetry

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/domain/live"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
)

func view(now time.Time) live.View {
	f := func(v float64) *float64 { return &v }
	iv := 60.0
	return live.View{
		Connected:    true,
		Capabilities: &observe.Capabilities{ServerVersion: "18.1", IsStandby: false},
		Activity:     &observe.Activity{At: now.Add(-5 * time.Second)},
		Points: map[string][]live.TimedValue{
			"sessions.active":          {{At: now, Value: f(3)}},
			"sessions.idle":            {{At: now, Value: f(7)}},
			"connections.usage_ratio":  {{At: now, Value: f(0.25)}},
			"db.size_bytes{shop}":      {{At: now, Value: f(1 << 20)}},
			"db.xid_age{shop}":         {{At: now, Value: nil}},  // unknown: not exported
			"statements.calls.per_s":   {{At: now, Value: f(5)}}, // a rate: exported as a counter only
			"some.unmapped_metric":     {{At: now, Value: f(1)}},
			"slots.retained_bytes{s1}": {{At: now, Value: f(42)}},
		},
		Totals: map[string]float64{"xact.commit.per_s": 1200, "statements.exec_ms.per_s": 2500, "deadlocks.delta": 2},
		Statements: []observe.StatementsCollection{{Interval: &iv, Deltas: []observe.QueryDelta{
			{Key: observe.QueryKey{QueryID: 7}, DatName: "shop", UserName: "app", Calls: 600, TotalMS: 1200},
			{Key: observe.QueryKey{QueryID: 8}, DatName: "shop", UserName: "app", Calls: 60, TotalMS: 6000},
		}}},
	}
}

func TestPrometheusExport(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	in := Input{Now: now, TopStatements: 1, Targets: []Target{{ID: 1, Name: "orders-db", Environment: "prod", IntervalSeconds: 10, View: view(now),
		Findings:  []repository.RuleCount{{TargetID: 1, RuleID: "vacuum.dead_tuples", Severity: "warning", Count: 2}},
		Incidents: []repository.RuleCount{{TargetID: 1, RuleID: "blocking.long_chain", Severity: "critical", Count: 1}}}}}
	var b bytes.Buffer
	if err := WritePrometheus(&b, Build(in)); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		`# TYPE locklens_target_up gauge`,
		`locklens_target_up{target="orders-db",environment="prod"} 1`,
		`locklens_target_info{target="orders-db",environment="prod",server_version="18.1",role="primary"} 1`,
		`locklens_pg_sessions{target="orders-db",environment="prod",state="active"} 3`,
		`locklens_pg_sessions{target="orders-db",environment="prod",state="idle"} 7`,
		`locklens_pg_database_size_bytes{target="orders-db",environment="prod",database="shop"} 1.048576e+06`,
		`locklens_pg_replication_slot_retained_bytes{target="orders-db",environment="prod",slot="s1"} 42`,
		`# TYPE locklens_pg_xact_commit_total counter`,
		`locklens_pg_statement_exec_seconds_total{target="orders-db",environment="prod"} 2.5`,
		`locklens_pg_deadlocks_total{target="orders-db",environment="prod"} 2`,
		`locklens_findings_active{target="orders-db",environment="prod",rule="vacuum.dead_tuples",severity="warning"} 2`,
		`locklens_incidents_open{target="orders-db",environment="prod",rule="blocking.long_chain",severity="critical"} 1`,
		// The top statement by time only, labelled by queryid.
		`locklens_pg_statement_mean_seconds{target="orders-db",environment="prod",queryid="8",database="shop",user="app"} 0.1`,
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("missing %s", want)
		}
	}
	for _, never := range []string{"xid_age", "unmapped", `queryid="7"`, "statement_calls_per_second{target=\"orders-db\",environment=\"prod\",queryid=\"7\""} {
		if strings.Contains(out, never) {
			t.Errorf("unexpected %s", never)
		}
	}
	// A stale instance is down.
	in.Targets[0].View.Activity.At = now.Add(-5 * time.Minute)
	if up(in.Targets[0], now) != 0 {
		t.Fatal("stale activity: down")
	}
}

func TestLabelEscaping(t *testing.T) {
	var b bytes.Buffer
	_ = WritePrometheus(&b, []Family{{Name: "x", Help: "a\\b", Type: Gauge, Samples: []Sample{{Labels: []Label{{"target", "we\"ird\n"}}, Value: 1}}}})
	if !strings.Contains(b.String(), `x{target="we\"ird\n"} 1`) || !strings.Contains(b.String(), `# HELP x a\\b`) {
		t.Fatalf("%q", b.String())
	}
}

func TestOTLPRequest(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	fams := []Family{{Name: "locklens_pg_xact_commit_total", Type: Counter, Samples: []Sample{{Labels: []Label{{"target", "a"}}, Value: 3}}},
		{Name: "locklens_target_up", Type: Gauge, Samples: []Sample{{Value: 1}}}}
	raw, _ := json.Marshal(otlpRequest(fams, "srv-1", "v1", now.Add(-time.Hour), now))
	s := string(raw)
	for _, want := range []string{`"isMonotonic":true`, `"aggregationTemporality":2`, `"startTimeUnixNano":"`, `"gauge":{"dataPoints"`,
		`{"key":"service.instance.id","value":{"stringValue":"srv-1"}}`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
}
