//go:build integration

package incidents_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/domain/incidents"
	"github.com/locklens/locklens/internal/domain/rules"
	"github.com/locklens/locklens/internal/lib/obs"
	"github.com/locklens/locklens/internal/repository"
)

// fences serves one server's view of its lease on the target.
type fences struct {
	f  repository.Fence
	ok bool
}

func (x *fences) Fence(int64) (repository.Fence, bool) { return x.f, x.ok }

type events struct{ kinds []string }

func (e *events) Publish(kind string, _ any) { e.kinds = append(e.kinds, kind) }

// Two servers hand a target over: the one that lost the lease records
// nothing more (not even while it still believes it holds it), the new one
// records the incident and its notification outbox row together.
func TestRecorderWritesAreFencedByTheLease(t *testing.T) {
	h := newHarness(t, incidents.DefaultConfig())
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	name := fmt.Sprintf("target:%d", h.target.ID)
	repos := incidents.Repositories{IncidentRepository: h.store, FindingRepository: h.store, OutboxRepository: h.store, FenceRepository: h.store}

	held, err := h.store.HoldLeases(ctx, []string{name}, "a", 50*time.Millisecond)
	if err != nil || len(held) != 1 {
		t.Fatalf("a's lease: %v %v", held, err)
	}
	fa := &fences{f: repository.Fence{TargetID: h.target.ID, Holder: "a", Epoch: held[name]}, ok: true}
	ma := obs.NewMetrics()
	evA := &events{}
	recA := incidents.NewRecorder(repos, h.cfg, thresholds{t: rules.Defaults()}, nil, fa, ma, evA, log)

	// a stalls; its lease expires and b takes the target over.
	time.Sleep(100 * time.Millisecond)
	held, err = h.store.HoldLeases(ctx, []string{name}, "b", time.Minute)
	if err != nil || len(held) != 1 || held[name] == fa.f.Epoch {
		t.Fatalf("b's lease: %v %v (a's epoch %d)", held, err, fa.f.Epoch)
	}
	fb := &fences{f: repository.Fence{TargetID: h.target.ID, Holder: "b", Epoch: held[name]}, ok: true}
	evB := &events{}
	recB := incidents.NewRecorder(repos, h.cfg, thresholds{t: rules.Defaults()}, nil, fb, obs.NewMetrics(), evB, log)

	// a, resuming, still thinks it holds the lease: everything it would
	// record is rejected by the store.
	at := t0
	lost := 0
	for range 8 {
		if recA.Analyze(ctx, h.target, view(at, true, 100)).LeaseLost {
			lost++
		}
		at = at.Add(step)
	}
	if lost != 8 || ma.FencedWrites.Total() != 8 {
		t.Fatalf("a: %d analyses reported the lease lost, %v fenced writes; want 8", lost, ma.FencedWrites.Total())
	}
	if n := len(h.incidents()); n != 0 || len(evA.kinds) != 0 {
		t.Fatalf("a recorded %d incidents and published %v after losing the lease", n, evA.kinds)
	}
	fs, err := h.store.ListFindings(ctx, repository.FindingFilter{TargetID: h.target.ID, Limit: 10})
	if err != nil || len(fs) != 0 {
		t.Fatalf("a recorded findings: %d %v", len(fs), err)
	}

	// b records the incident, and its outbox row in the same transaction.
	at = t0
	for range 8 {
		if recB.Analyze(ctx, h.target, view(at, true, 100)).LeaseLost {
			t.Fatal("b holds the lease")
		}
		at = at.Add(step)
	}
	list := h.incidents()
	if len(list) != 1 {
		t.Fatalf("b: %d incidents", len(list))
	}
	items, err := h.store.ClaimNotifications(ctx, "sender", 10, time.Minute)
	if err != nil || len(items) == 0 || items[0].IncidentID != list[0].ID || items[0].Event != "incident.opened" {
		t.Fatalf("outbox: %+v %v", items, err)
	}
	if len(evB.kinds) == 0 || evB.kinds[0] != "incident.opened" {
		t.Fatalf("b's events: %v", evB.kinds)
	}

	// A server that knows it lost the lease (self-fenced) does not even try.
	fb.ok = false
	if !recB.Analyze(ctx, h.target, view(at, true, 100)).LeaseLost {
		t.Fatal("a self-fenced analysis must report the lease lost")
	}
}

// Targets are analysed concurrently (one lock per target): several targets
// fed at once each get their incident, and the open gauge counts them all.
func TestTargetsAreAnalysedConcurrently(t *testing.T) {
	h := newHarness(t, incidents.DefaultConfig())
	ctx := context.Background()
	m := obs.NewMetrics()
	rec := incidents.NewRecorder(incidents.Repositories{IncidentRepository: h.store, FindingRepository: h.store, OutboxRepository: h.store,
		FenceRepository: h.store}, h.cfg, thresholds{t: rules.Defaults()}, nil, nil, m, nopSink{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	targets := []repository.Target{h.target}
	for i := range 5 {
		tg, err := h.store.CreateTarget(ctx, repository.Target{Name: fmt.Sprintf("c%d", i), DSNCiphertext: []byte{1}, DisplayHost: "h",
			DisplayPort: 5432, DisplayDatabase: "d", DisplayUser: "u", Enabled: true, IntervalSeconds: int(step / time.Second), MaxConnections: 1})
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, tg)
	}
	var wg sync.WaitGroup
	for _, tg := range targets {
		wg.Go(func() {
			at := t0
			for range 8 {
				rec.Analyze(ctx, tg, view(at, true, 100))
				at = at.Add(step)
			}
		})
	}
	wg.Wait()
	for _, tg := range targets {
		list, _, err := h.store.ListIncidents(ctx, repository.IncidentFilter{TargetID: tg.ID, RuleID: rules.RuleBlockingChain, Limit: 10})
		if err != nil || len(list) != 1 {
			t.Fatalf("target %d: %d incidents %v", tg.ID, len(list), err)
		}
	}
	if got := m.IncidentsOpen.Total(); got != float64(len(targets)) {
		t.Fatalf("open incidents gauge %v, want %d", got, len(targets))
	}
	rec.Forget(targets[0].ID)
	if got := m.IncidentsOpen.Total(); got != float64(len(targets)-1) {
		t.Fatalf("after forgetting one target: %v", got)
	}
}
