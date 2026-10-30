package export

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/domain/finding"
	"github.com/locklens/locklens/internal/domain/model"
	"github.com/locklens/locklens/internal/repository"
)

var at = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

// fakeSource serves one incident with its events and findings.
type fakeSource struct {
	repository.IncidentRepository
	repository.FindingRepository
	in     repository.Incident
	events []repository.IncidentEvent
	fs     []repository.FindingRow
}

func (f fakeSource) GetIncident(context.Context, int64) (repository.Incident, error) {
	return f.in, nil
}
func (f fakeSource) ListIncidentEvents(context.Context, int64, int64, int) ([]repository.IncidentEvent, error) {
	return f.events, nil
}
func (f fakeSource) ListFindings(context.Context, repository.FindingFilter) ([]repository.FindingRow, error) {
	return f.fs, nil
}

func ptr[T any](v T) *T { return &v }

func source(t *testing.T) Repositories {
	t.Helper()
	snap := model.Snapshot{SchemaVersion: 1, ServerTime: at, CollectedAt: at,
		Sessions: []model.Session{
			{PID: 1, State: ptr("idle in transaction"), DetailsVisible: true, Query: ptr("UPDATE secret_table SET x = 'cnry-sql'"), ApplicationName: ptr("<script>alert(1)</script>"), WaitingLocks: []model.WaitingLock{}},
			{PID: 2, State: ptr("active"), DetailsVisible: true, WaitingLocks: []model.WaitingLock{{PID: 2, LockType: "transactionid", Mode: "ShareLock", TransactionID: ptr("9")}}},
		},
		Edges: []model.Edge{{Blocker: 1, Waiter: 2}}}
	raw, _ := json.Marshal(map[string]any{"snapshot": snap})
	f := finding.Finding{RuleID: "blocking.long_chain", RuleVersion: 1, Key: "pid:1", Severity: finding.Warning,
		Title: `Blocking <img src=x onerror=alert(1)>`, Status: finding.Active, ObservedFrom: at, ObservedTo: at,
		Evidence: []finding.Evidence{{Label: "root pid", Value: 1, Source: "pg_blocking_pids"}}, Explanation: "e", Limitations: "l"}
	trig, _ := json.Marshal(map[string]any{"finding": f})
	work, _ := json.Marshal(map[string]any{"current": []map[string]any{{"key": map[string]any{"queryid": "7"}, "datname": "app", "username": "u", "text": "SELECT cnry-workload", "calls": 5, "total_exec_ms": 10.0}}})
	f2 := fakeSource{
		in: repository.Incident{ID: 3, TargetName: "prod", RuleID: "blocking.long_chain", RuleVersion: 1, Severity: "warning",
			Title: "Blocking <b>chain</b>", State: "resolved", OpenedAt: at, LastSeenAt: at.Add(time.Minute), Source: "recorder",
			TriggerConfig: json.RawMessage("{}")},
		events: []repository.IncidentEvent{
			{ID: 1, At: at, Kind: "trigger", Summary: "Triggered", Payload: trig},
			{ID: 2, At: at, Kind: "blocking_snapshot", Summary: "graph", Payload: raw},
			{ID: 3, At: at, Kind: "workload", Summary: "top", Payload: work},
		},
		fs: []repository.FindingRow{{ID: 1, Finding: f}},
	}
	return Repositories{IncidentRepository: f2, FindingRepository: f2}
}

func TestBuildScrubsQueryTextByDefault(t *testing.T) {
	e, err := Build(context.Background(), source(t), 3, false, model.Tool{Name: "locklens", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteJSON(&buf, e); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "cnry-sql") || strings.Contains(buf.String(), "cnry-workload") {
		t.Fatal("query text must not be exported without include_query")
	}
	e2, _ := Build(context.Background(), source(t), 3, true, model.Tool{})
	buf.Reset()
	WriteJSON(&buf, e2)
	if !strings.Contains(buf.String(), "cnry-sql") || !e2.QueryTextIncluded {
		t.Fatal("include_query must keep query text")
	}
	if strings.Contains(buf.String(), "postgres://") || strings.Contains(strings.ToLower(buf.String()), "password") {
		t.Fatal("export must not contain connection strings")
	}
}

func TestHTMLIsStandaloneAndEscaped(t *testing.T) {
	e, _ := Build(context.Background(), source(t), 3, false, model.Tool{Name: "locklens", Version: "test"})
	var buf bytes.Buffer
	if err := WriteHTML(&buf, e); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, bad := range []string{"<script", "<img src=x", "<b>chain</b>", "http://", "https://", "cnry-sql"} {
		if strings.Contains(out, bad) {
			t.Errorf("HTML contains %q", bad)
		}
	}
	for _, want := range []string{"&lt;script&gt;", "pid 1", "pid 2", "ShareLock on transaction 9", "blocking.long_chain", "Blocking graphs", "Timeline", "not confirmed"} {
		if !strings.Contains(out, want) {
			t.Errorf("HTML lacks %q", want)
		}
	}
}

func TestDecodeValidates(t *testing.T) {
	e, _ := Build(context.Background(), source(t), 3, false, model.Tool{})
	var buf bytes.Buffer
	WriteJSON(&buf, e)
	if _, err := Decode(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	cases := map[string]string{
		`{"format":"other","schema_version":1}`:                                     "not a LockLens incident export",
		`{"format":"locklens-incident","schema_version":9}`:                         "unsupported incident export schema_version 9",
		`{"format":"locklens-incident","schema_version":1,"incident":{"title":""}}`: "title is missing",
		`not json`: "not valid JSON",
	}
	for in, want := range cases {
		if _, err := Decode(strings.NewReader(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Decode(%q) = %v, want %q", in, err, want)
		}
	}
	big := bytes.Repeat([]byte(" "), MaxImportBytes+10)
	if _, err := Decode(bytes.NewReader(big)); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("oversized input: %v", err)
	}
}
