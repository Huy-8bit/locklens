package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
)

func TestStatementKeys(t *testing.T) {
	want := observe.QueryKey{ServerMajor: 18, DBID: 16384, UserID: 10, TopLevel: true, QueryID: -6328518588233945134}
	if k, ok := parseFindingKey("q:18:16384:10:true:-6328518588233945134"); !ok || k != want {
		t.Fatalf("parseFindingKey = %+v, %v", k, ok)
	}
	for _, bad := range []string{"root:42", "_", "q:18:x"} {
		if _, ok := parseFindingKey(bad); ok {
			t.Fatalf("parseFindingKey(%q) accepted", bad)
		}
	}
	other := observe.QueryKey{ServerMajor: 18, DBID: 16384, UserID: 10, TopLevel: true, QueryID: 7}
	workload, _ := json.Marshal(map[string]any{"current": []map[string]any{{"key": want}, {"key": other}}})
	evs := []repository.IncidentEvent{
		{Kind: "trigger", Payload: json.RawMessage(`{"finding":{"key":"q:18:16384:10:true:-6328518588233945134"}}`)},
		{Kind: "trigger", Payload: json.RawMessage(`{"finding":{"key":"root:42"}}`)},
		{Kind: "workload", Payload: workload},
		{Kind: "sample", Payload: json.RawMessage(`{}`)},
	}
	got := statementKeys(evs)
	if len(got) != 2 || got[0] != want || got[1] != other {
		t.Fatalf("statementKeys = %+v", got)
	}
}
