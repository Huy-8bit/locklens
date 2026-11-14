//go:build integration

package repository_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/repository"
)

func incident(t *testing.T, s repository.IncidentRepository, tg repository.Target, title string) int64 {
	t.Helper()
	now := time.Now().UTC()
	id, err := s.CreateIncident(context.Background(), repository.Incident{TargetID: &tg.ID, TargetName: tg.Name, RuleID: "blocking.long_chain", RuleVersion: 1,
		Severity: "warning", Title: title, State: "open", OpenedAt: now, LastSeenAt: now, TriggerConfig: json.RawMessage(`{}`), Source: "recorder"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// A lease handed over between two servers: the old holder's fenced writes
// are rejected (incidents, statuses, live views), the new holder's pass, and
// a failing fenced transaction leaves nothing behind.
func TestLeaseHandoverFencesTheOldHolder(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	tg := target(t, s, "fenced")
	name := fmt.Sprintf("target:%d", tg.ID)

	held, err := s.HoldLeases(ctx, []string{name}, "a", 100*time.Millisecond)
	if err != nil || len(held) != 1 {
		t.Fatalf("a: %v %v", held, err)
	}
	fa := repository.Fence{TargetID: tg.ID, Holder: "a", Epoch: held[name]}
	if got, _ := s.HoldLeases(ctx, []string{name}, "b", time.Minute); len(got) != 0 {
		t.Fatal("b took a live lease")
	}
	// a renews: same epoch.
	if again, _ := s.HoldLeases(ctx, []string{name}, "a", 100*time.Millisecond); again[name] != fa.Epoch {
		t.Fatalf("renewal changed the epoch: %d → %d", fa.Epoch, again[name])
	}
	if err := s.Fenced(ctx, fa, func(tx repository.FencedWrites) error {
		id := incident(t, tx, tg, "by a")
		return tx.EnqueueNotification(ctx, id, "incident.opened")
	}); err != nil {
		t.Fatalf("a's fenced write: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	held, err = s.HoldLeases(ctx, []string{name}, "b", time.Minute)
	if err != nil || len(held) != 1 || held[name] == fa.Epoch {
		t.Fatalf("b takes over with a new epoch: %v %v (a's %d)", held, err, fa.Epoch)
	}
	fb := repository.Fence{TargetID: tg.ID, Holder: "b", Epoch: held[name]}

	// The old holder is rejected and its function never runs.
	ran := false
	err = s.Fenced(ctx, fa, func(tx repository.FencedWrites) error {
		ran = true
		return nil
	})
	if !errors.Is(err, repository.ErrLeaseLost) || ran {
		t.Fatalf("a after the handover: %v (ran %v)", err, ran)
	}
	st := repository.TargetStatus{TargetID: tg.ID, State: "healthy"}
	if failed, fenced, err := s.SaveTargetStatuses(ctx, []repository.TargetStatus{st}, []repository.Fence{fa}); err != nil || len(failed) != 0 || len(fenced) != 1 {
		t.Fatalf("a's status: failed %v fenced %v %v", failed, fenced, err)
	}
	if _, err := s.GetTargetStatus(ctx, tg.ID); !errors.Is(err, repository.ErrNotFound) {
		// target() may have created a status row; then it must not say healthy.
		if got, _ := s.GetTargetStatus(ctx, tg.ID); got.State == "healthy" {
			t.Fatal("a's fenced status was written")
		}
	}
	if w, err := s.SaveLiveStates(ctx, "a", time.Now(), []int64{tg.ID}, [][]byte{[]byte(`{}`)}, []int64{fa.Epoch}); err != nil || len(w) != 0 {
		t.Fatalf("a's live view: %v %v", w, err)
	}
	// The new holder's writes pass.
	if failed, fenced, err := s.SaveTargetStatuses(ctx, []repository.TargetStatus{st}, []repository.Fence{fb}); err != nil || len(failed)+len(fenced) != 0 {
		t.Fatalf("b's status: failed %v fenced %v %v", failed, fenced, err)
	}
	if got, err := s.GetTargetStatus(ctx, tg.ID); err != nil || got.State != "healthy" {
		t.Fatalf("b's status: %+v %v", got, err)
	}
	if w, err := s.SaveLiveStates(ctx, "b", time.Now(), []int64{tg.ID}, [][]byte{[]byte(`{}`)}, []int64{fb.Epoch}); err != nil || len(w) != 1 {
		t.Fatalf("b's live view: %v %v", w, err)
	}
	// A failing fenced transaction writes nothing, outbox included.
	before, _, _ := s.PendingNotifications(ctx)
	boom := errors.New("boom")
	err = s.Fenced(ctx, fb, func(tx repository.FencedWrites) error {
		id := incident(t, tx, tg, "rolled back")
		if err := tx.EnqueueNotification(ctx, id, "incident.opened"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("fn error: %v", err)
	}
	after, _, _ := s.PendingNotifications(ctx)
	list, _, _ := s.ListIncidents(ctx, repository.IncidentFilter{TargetID: tg.ID, Limit: 10})
	if after != before || len(list) != 1 {
		t.Fatalf("rolled back transaction left outbox %d → %d, incidents %d", before, after, len(list))
	}
	// A lease released and taken again never reuses an epoch.
	if err := s.ReleaseLeases(ctx, []string{name}, "b"); err != nil {
		t.Fatal(err)
	}
	again, _ := s.HoldLeases(ctx, []string{name}, "a", time.Minute)
	if again[name] == fa.Epoch || again[name] == fb.Epoch {
		t.Fatalf("epoch %d reused", again[name])
	}
}

// While a fenced transaction runs, the lease cannot change holder, even if
// it expires meanwhile: the takeover waits for the commit, so the old
// holder's writes never land after the new holder's.
func TestFencedTransactionHoldsOffTheTakeover(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	tg := target(t, s, "holdoff")
	name := fmt.Sprintf("target:%d", tg.ID)
	held, _ := s.HoldLeases(ctx, []string{name}, "a", 100*time.Millisecond)
	fa := repository.Fence{TargetID: tg.ID, Holder: "a", Epoch: held[name]}

	inside := make(chan struct{})
	var committed time.Time
	done := make(chan error, 1)
	go func() {
		done <- s.Fenced(ctx, fa, func(tx repository.FencedWrites) error {
			close(inside)
			time.Sleep(500 * time.Millisecond) // the lease expires meanwhile
			incident(t, tx, tg, "slow")
			committed = time.Now()
			return nil
		})
	}()
	<-inside
	time.Sleep(200 * time.Millisecond)
	got, err := s.HoldLeases(ctx, []string{name}, "b", time.Minute)
	took := time.Now()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err != nil || len(got) != 1 {
		t.Fatalf("b: %v %v", got, err)
	}
	if took.Before(committed) {
		t.Fatalf("b took the lease at %v, before a's transaction committed at %v", took, committed)
	}
}

// Outbox claims: all rows of an incident go to one sender, an incident
// another sender is processing is skipped, and concurrent senders never
// claim the same incident.
func TestOutboxClaims(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	tg := target(t, s, "outbox")
	i1, i2 := incident(t, s, tg, "one"), incident(t, s, tg, "two")
	for _, e := range []struct {
		id   int64
		kind string
	}{{i1, "incident.opened"}, {i2, "incident.opened"}, {i1, "incident.updated"}} {
		if err := s.EnqueueNotification(ctx, e.id, e.kind); err != nil {
			t.Fatal(err)
		}
	}
	x, err := s.ClaimNotifications(ctx, "x", 1, time.Minute)
	if err != nil || len(x) != 2 || x[0].IncidentID != i1 || x[1].IncidentID != i1 {
		t.Fatalf("x claims incident one's rows: %+v %v", x, err)
	}
	// A new change of incident one while x processes it: not for y.
	if err := s.EnqueueNotification(ctx, i1, "incident.resolved"); err != nil {
		t.Fatal(err)
	}
	y, err := s.ClaimNotifications(ctx, "y", 10, time.Minute)
	if err != nil || len(y) != 1 || y[0].IncidentID != i2 {
		t.Fatalf("y: %+v %v", y, err)
	}
	// x (restarted) claims its rows again, with the new one.
	x, _ = s.ClaimNotifications(ctx, "x", 10, time.Minute)
	if len(x) != 3 {
		t.Fatalf("x again: %+v", x)
	}
	ids := []int64{x[0].ID, x[1].ID, x[2].ID}
	if err := s.DoneNotifications(ctx, ids, "y"); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := s.PendingNotifications(ctx); n != 4 {
		t.Fatalf("another sender removed x's rows: %d left", n)
	}
	if err := s.DoneNotifications(ctx, ids, "x"); err != nil {
		t.Fatal(err)
	}
	// y stopped: its claim is taken over once stale.
	if z, _ := s.ClaimNotifications(ctx, "z", 10, time.Minute); len(z) != 0 {
		t.Fatalf("z claimed y's fresh rows: %+v", z)
	}
	z, _ := s.ClaimNotifications(ctx, "z", 10, 0)
	if len(z) != 1 || z[0].IncidentID != i2 {
		t.Fatalf("z after y's claim went stale: %+v", z)
	}
	if err := s.DoneNotifications(ctx, []int64{z[0].ID}, "z"); err != nil {
		t.Fatal(err)
	}

	// Concurrent senders draining a backlog never share an incident.
	var all []int64
	for i := range 40 {
		id := incident(t, s, tg, fmt.Sprintf("bulk %d", i))
		all = append(all, id)
		for range 2 {
			if err := s.EnqueueNotification(ctx, id, "incident.updated"); err != nil {
				t.Fatal(err)
			}
		}
	}
	var mu sync.Mutex
	claimedBy := map[int64]map[string]bool{}
	var wg sync.WaitGroup
	for _, sender := range []string{"s1", "s2", "s3", "s4"} {
		wg.Go(func() {
			for range 200 {
				items, err := s.ClaimNotifications(ctx, sender, 3, time.Minute)
				if err != nil {
					continue // a deadlock between two claims: retried
				}
				if len(items) == 0 {
					return
				}
				var done []int64
				mu.Lock()
				for _, it := range items {
					if claimedBy[it.IncidentID] == nil {
						claimedBy[it.IncidentID] = map[string]bool{}
					}
					claimedBy[it.IncidentID][sender] = true
					done = append(done, it.ID)
				}
				mu.Unlock()
				time.Sleep(5 * time.Millisecond) // processing
				if err := s.DoneNotifications(ctx, done, sender); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	for _, id := range all {
		if len(claimedBy[id]) != 1 {
			t.Fatalf("incident %d claimed by %v", id, claimedBy[id])
		}
	}
	if n, _, _ := s.PendingNotifications(ctx); n != 0 {
		t.Fatalf("%d rows left", n)
	}
}
