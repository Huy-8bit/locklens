package incidents

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/domain/live"
	"github.com/locklens/locklens/internal/domain/observe"
)

func TestLoadEventSummarisesRecentBuckets(t *testing.T) {
	now := time.Date(2026, 10, 25, 13, 0, 0, 0, time.UTC)
	v := live.View{Load: []observe.LoadBucket{
		// Older than the window: ignored.
		{At: now.Add(-10 * time.Minute), Ticks: 100, Groups: []observe.LoadGroup{{WaitClass: "CPU", QueryID: 1, Samples: 1000}}},
		{At: now.Add(-time.Minute), Ticks: 10, Groups: []observe.LoadGroup{
			{WaitClass: "Lock", WaitEvent: "transactionid", DBID: 5, UserID: 10, QueryID: 42, Samples: 30},
			{WaitClass: "CPU", DBID: 5, UserID: 10, QueryID: 42, Samples: 10},
			{WaitClass: "CPU", DBID: 5, UserID: 10, QueryID: 7, Samples: 5},
			{WaitClass: observe.WaitIdleTx, DBID: 5, UserID: 10, QueryID: 9, Samples: 20},
		}},
	}}
	ev := loadEvent(v, now)
	if ev == nil || ev.Kind != KindLoad {
		t.Fatalf("event = %+v", ev)
	}
	var p struct {
		Ticks      int                `json:"ticks"`
		ActiveAAS  float64            `json:"active_aas"`
		Classes    map[string]float64 `json:"classes"`
		Waits      []loadWait         `json:"waits"`
		Statements []loadStatement    `json:"statements"`
	}
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Ticks != 10 || p.ActiveAAS != 4.5 || p.Classes[observe.WaitIdleTx] != 2 {
		t.Fatalf("payload = %+v", p)
	}
	if p.Waits[0].Class != "Lock" || p.Waits[0].AAS != 3 {
		t.Fatalf("waits = %+v", p.Waits)
	}
	// Idle-in-transaction sessions are not load: query 9 is not listed.
	if len(p.Statements) != 2 || p.Statements[0].QueryID != 42 || p.Statements[0].AAS != 4 || p.Statements[0].Classes["Lock"] != 3 {
		t.Fatalf("statements = %+v", p.Statements)
	}
	if loadEvent(live.View{}, now) != nil {
		t.Fatal("no samples must give no event")
	}
}
