package report

import (
	"strings"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/domain/model"
)

var now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

func ago(d time.Duration) *time.Time { return ptr(now.Add(-d)) }

func session(pid int, state string, xactAge time.Duration) model.Session {
	s := model.Session{
		PID:            pid,
		State:          ptr(state),
		BackendType:    ptr("client backend"),
		DetailsVisible: true,
		WaitingLocks:   []model.WaitingLock{},
	}
	if xactAge > 0 {
		s.XactStart = ago(xactAge)
	}
	return s
}

func snapshot(sessions []model.Session, edges ...model.Edge) *model.Snapshot {
	return &model.Snapshot{
		SchemaVersion: model.SchemaVersion,
		ServerTime:    now,
		CollectedAt:   now,
		Sessions:      sessions,
		Edges:         edges,
	}
}

func findNode(t *testing.T, r *Report, id int) Node {
	t.Helper()
	for _, n := range r.Analysis.Nodes {
		if n.ID == id {
			return n
		}
	}
	t.Fatalf("node %d not found", id)
	return Node{}
}

func hasWarning(r *Report, code string) bool {
	for _, w := range r.Analysis.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

func TestChainSummaryAndKinds(t *testing.T) {
	snap := snapshot([]model.Session{
		session(1, "idle in transaction", 10*time.Minute),
		session(2, "active", 5*time.Minute),
		session(3, "active", time.Minute),
	}, model.Edge{Blocker: 1, Waiter: 2}, model.Edge{Blocker: 2, Waiter: 3})

	r := Build(snap, 2*time.Minute)
	s := r.Analysis.Summary
	if s.BlockedSessions != 2 || s.BlockingSessions != 2 || s.RootBlockers != 1 {
		t.Errorf("summary = %+v", s)
	}
	if s.LongTransactions != 2 || s.IdleInTransaction != 1 {
		t.Errorf("long transactions = %d, idle = %d", s.LongTransactions, s.IdleInTransaction)
	}
	if n := findNode(t, r, 1); n.Kind != KindSession || n.TransitiveBlocked != 2 || !n.Root {
		t.Errorf("node 1 = %+v", n)
	}
	// Long transactions are ordered oldest first and carry blocking counts.
	lt := r.Analysis.LongTransactions
	if lt[0].PID != 1 || !lt[0].IdleInTransaction || lt[0].DirectBlocked != 1 {
		t.Errorf("long transactions = %+v", lt)
	}
}

func TestMissingAndPreparedNodes(t *testing.T) {
	snap := snapshot([]model.Session{session(10, "active", time.Second)},
		model.Edge{Blocker: 0, Waiter: 10}, model.Edge{Blocker: 99, Waiter: 10})
	snap.PreparedTransactions = []model.PreparedTransaction{{GID: "g1"}}
	r := Build(snap, time.Hour)

	if n := findNode(t, r, 0); n.Kind != KindPreparedTransaction {
		t.Errorf("PID 0 kind = %s", n.Kind)
	}
	if n := findNode(t, r, 99); n.Kind != KindMissing {
		t.Errorf("PID 99 kind = %s", n.Kind)
	}
	if !hasWarning(r, model.WarnMissingSession) || !hasWarning(r, model.WarnPreparedBlocker) {
		t.Errorf("warnings = %+v", r.Analysis.Warnings)
	}
}

func TestCycleWarningAndNoRoots(t *testing.T) {
	snap := snapshot([]model.Session{session(1, "active", time.Second), session(2, "active", time.Second)},
		model.Edge{Blocker: 1, Waiter: 2}, model.Edge{Blocker: 2, Waiter: 1})
	r := Build(snap, time.Hour)
	if r.Analysis.Summary.RootBlockers != 0 || r.Analysis.Summary.CyclicComponents != 1 {
		t.Errorf("summary = %+v", r.Analysis.Summary)
	}
	if !hasWarning(r, model.WarnCycle) {
		t.Fatal("missing cycle warning")
	}
	for _, w := range r.Analysis.Warnings {
		if w.Code == model.WarnCycle && !strings.Contains(w.Message, "not a confirmed deadlock") {
			t.Errorf("cycle warning must not claim a deadlock: %s", w.Message)
		}
	}
}

func TestNullableTimestampsYieldUnknownAges(t *testing.T) {
	hidden := model.Session{PID: 5, DetailsVisible: false} // every timestamp NULL
	r := Build(snapshot([]model.Session{hidden}), 0)
	a := r.Analysis.Sessions[0]
	if a.TransactionAgeSeconds != nil || a.QueryAgeSeconds != nil || a.StateAgeSeconds != nil || a.LockWaitSeconds != nil {
		t.Errorf("expected all ages unknown, got %+v", a)
	}
	if len(r.Analysis.LongTransactions) != 0 {
		t.Error("a session without xact_start cannot be a long transaction")
	}
}

func TestAgesAreDistinct(t *testing.T) {
	s := session(7, "active", 10*time.Minute)
	s.QueryStart = ago(3 * time.Minute)
	s.StateChange = ago(3 * time.Minute)
	s.WaitingLocks = []model.WaitingLock{
		{LockType: "transactionid", Mode: "ShareLock", WaitStart: ago(90 * time.Second)},
		{LockType: "tuple", Mode: "ExclusiveLock", WaitStart: nil},
	}
	r := Build(snapshot([]model.Session{s}), time.Hour)
	a := r.Analysis.Sessions[0]
	if *a.TransactionAgeSeconds != 600 || *a.QueryAgeSeconds != 180 || *a.LockWaitSeconds != 90 {
		t.Errorf("ages = tx %v query %v lock %v", *a.TransactionAgeSeconds, *a.QueryAgeSeconds, *a.LockWaitSeconds)
	}
}

func TestLockWaitUnknownWithoutWaitStart(t *testing.T) {
	s := session(7, "active", time.Minute)
	s.QueryStart = ago(time.Minute)
	s.WaitingLocks = []model.WaitingLock{{LockType: "relation", Mode: "AccessExclusiveLock"}}
	r := Build(snapshot([]model.Session{s}), time.Hour)
	if r.Analysis.Sessions[0].LockWaitSeconds != nil {
		t.Error("lock wait must stay unknown when waitstart is NULL; query_start is not a substitute")
	}
}

func TestThresholdBoundaryAndBackendType(t *testing.T) {
	exact := session(1, "idle in transaction", 30*time.Second)
	below := session(2, "idle in transaction", 29*time.Second)
	worker := session(3, "active", time.Hour)
	worker.BackendType = ptr("autovacuum worker")
	aborted := session(4, "idle in transaction (aborted)", time.Hour)
	r := Build(snapshot([]model.Session{exact, below, worker, aborted}), 30*time.Second)

	got := map[int]bool{}
	for _, lt := range r.Analysis.LongTransactions {
		got[lt.PID] = lt.IdleInTransaction
	}
	if _, ok := got[1]; !ok {
		t.Error("transaction exactly at the threshold must be reported")
	}
	if _, ok := got[2]; ok {
		t.Error("transaction below the threshold must not be reported")
	}
	if _, ok := got[3]; ok {
		t.Error("only client backends are listed as long transactions")
	}
	if !got[4] {
		t.Error("idle in transaction (aborted) counts as idle in transaction")
	}
}

func TestFutureTimestampsClampToZero(t *testing.T) {
	s := session(1, "active", 0)
	s.XactStart = ptr(now.Add(time.Second))
	r := Build(snapshot([]model.Session{s}), 0)
	if *r.Analysis.Sessions[0].TransactionAgeSeconds != 0 {
		t.Error("ages must never be negative")
	}
}

func TestEmptySnapshotProducesEmptySlices(t *testing.T) {
	r := Build(snapshot([]model.Session{}), time.Minute)
	if r.Analysis.Nodes == nil || r.Analysis.Edges == nil || r.Analysis.LongTransactions == nil || r.Analysis.Warnings == nil {
		t.Error("JSON consumers expect [] rather than null")
	}
}
