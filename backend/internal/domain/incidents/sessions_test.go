package incidents

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/domain/live"
	"github.com/locklens/locklens/internal/domain/model"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/domain/report"
)

func TestSessionsEventOrdersBlockersFirst(t *testing.T) {
	now := time.Date(2026, 10, 25, 13, 0, 0, 0, time.UTC)
	str := func(s string) *string { return &s }
	at := func(d time.Duration) *time.Time { x := now.Add(-d); return &x }
	client := str("client backend")
	snap := &model.Snapshot{ServerTime: now, CollectedAt: now, Collection: model.Collection{IncludeQuery: true},
		Sessions: []model.Session{
			{PID: 10, BackendType: client, State: str("active"), QueryStart: at(5 * time.Second), XactStart: at(5 * time.Second), Query: str("SELECT slow()"), DetailsVisible: true},
			{PID: 20, BackendType: client, State: str("idle in transaction"), QueryStart: at(time.Minute), XactStart: at(2 * time.Minute), Query: str("UPDATE t SET x = 1"), DetailsVisible: true},
			{PID: 30, BackendType: client, State: str("active"), QueryStart: at(30 * time.Second), XactStart: at(30 * time.Second), Query: str("UPDATE t SET x = 2"), DetailsVisible: true,
				WaitingLocks: []model.WaitingLock{{PID: 30, LockType: "transactionid", Mode: "ShareLock"}}},
			{PID: 40, BackendType: str("autovacuum worker"), State: str("active"), DetailsVisible: true},
		},
		Edges: []model.Edge{{Blocker: 20, Waiter: 30}},
	}
	v := live.View{Activity: &observe.Activity{At: now, Snapshot: snap,
		Counts: observe.ActivityCounts{ClientTotal: 12, Active: 2, Idle: 9, IdleInTx: 1, WaitingLock: 1}}, Report: report.Build(snap, time.Minute)}

	ev := sessionsEvent(v, now)
	if ev == nil || ev.Kind != KindSessions {
		t.Fatalf("event = %+v", ev)
	}
	var p struct {
		Counts   sessionCounts  `json:"counts"`
		Sessions []sessionBrief `json:"sessions"`
	}
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		t.Fatal(err)
	}
	var pids []int
	for _, s := range p.Sessions {
		pids = append(pids, s.PID)
	}
	// The blocker first, then by running time; the autovacuum worker is not a client.
	if want := []int{20, 30, 10}; len(pids) != len(want) || pids[0] != want[0] || pids[1] != want[1] || pids[2] != want[2] {
		t.Fatalf("order = %v, want %v", pids, want)
	}
	if p.Sessions[0].Blocks != 1 || len(p.Sessions[1].BlockedBy) != 1 || p.Sessions[1].BlockedBy[0] != 20 {
		t.Fatalf("blocking = %+v / %+v", p.Sessions[0], p.Sessions[1])
	}
	if p.Counts.Client != 12 || p.Counts.Active != 2 || p.Counts.Listed != 3 {
		t.Fatalf("counts = %+v", p.Counts)
	}
	if p.Sessions[2].Query == nil || *p.Sessions[2].Query != "SELECT slow()" {
		t.Fatalf("query text missing: %+v", p.Sessions[2])
	}
}
