package cluster

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/repository"
)

// memStore holds leases and heartbeats in memory, on a fake clock.
type memStore struct {
	mu      sync.Mutex
	now     time.Time
	beats   map[string]time.Time
	leases  map[string]repository.Lease
	notices []string
}

func newMem() *memStore {
	return &memStore{now: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC), beats: map[string]time.Time{}, leases: map[string]repository.Lease{}}
}

func (m *memStore) Heartbeat(_ context.Context, sv repository.Server) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.beats[sv.ID] = m.now
	return nil
}

func (m *memStore) Servers(_ context.Context, ttl time.Duration) ([]repository.Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []repository.Server
	for id, at := range m.beats {
		out = append(out, repository.Server{ID: id, HeartbeatAt: at, Alive: !at.Before(m.now.Add(-ttl))})
	}
	return out, nil
}

func (m *memStore) HoldLease(_ context.Context, name, holder string, ttl time.Duration) (bool, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leases[name]
	if ok && l.Holder != holder && !l.ExpiresAt.Before(m.now) {
		return false, time.Time{}, nil
	}
	if !ok || l.Holder != holder {
		l = repository.Lease{Name: name, Holder: holder, AcquiredAt: m.now}
	}
	l.ExpiresAt = m.now.Add(ttl)
	m.leases[name] = l
	return true, l.AcquiredAt, nil
}

func (m *memStore) ReleaseLease(_ context.Context, name, holder string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.leases[name]; ok && l.Holder == holder {
		delete(m.leases, name)
	}
	return nil
}

func (m *memStore) Leases(_ context.Context, prefix string) ([]repository.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []repository.Lease
	for name, l := range m.leases {
		if strings.HasPrefix(name, prefix) {
			l.Live = !l.ExpiresAt.Before(m.now)
			out = append(out, l)
		}
	}
	return out, nil
}

func (m *memStore) Notify(_ context.Context, _, payload string) error {
	m.notices = append(m.notices, payload)
	return nil
}

func (m *memStore) Listen(ctx context.Context, _ string, _ func(string)) error {
	<-ctx.Done()
	return nil
}

func (m *memStore) advance(d time.Duration) {
	m.mu.Lock()
	m.now = m.now.Add(d)
	m.mu.Unlock()
}

func TestTargetsAreSplitAndTakenOver(t *testing.T) {
	ctx := context.Background()
	st := newMem()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, b := New(st, "a", "h", "v", log), New(st, "b", "h", "v", log)
	targets := []int64{1, 2, 3, 4, 5}

	// Alone, a server collects everything.
	a.beat(ctx)
	if got, _ := a.Assign(ctx, targets); len(got) != 5 {
		t.Fatalf("alone: %v", got)
	}
	// A second server joins: a gives up targets beyond its share, b takes
	// them on its next sync.
	b.beat(ctx)
	a.beat(ctx)
	ga, _ := a.Assign(ctx, targets)
	gb, _ := b.Assign(ctx, targets)
	if len(ga) != 3 || len(gb) != 2 {
		t.Fatalf("split: a %v b %v", ga, gb)
	}
	for id := range ga {
		if gb[id] {
			t.Fatalf("target %d collected twice", id)
		}
	}
	// a stops: once its heartbeat and leases expire, b takes everything.
	st.advance(targetLeaseTTL + time.Second)
	b.beat(ctx)
	if got, _ := b.Assign(ctx, targets); len(got) != 5 {
		t.Fatalf("takeover: %v", got)
	}
	// A deleted target's lease is released.
	if got, _ := b.Assign(ctx, targets[:4]); len(got) != 4 {
		t.Fatalf("after delete: %v", got)
	}
	if ls, _ := st.Leases(ctx, targetPrefix); len(ls) != 4 {
		t.Fatalf("leases %v", ls)
	}
}

func TestSingletonRunsOnOneServer(t *testing.T) {
	st := newMem()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	running := map[string]bool{}
	for _, id := range []string{"a", "b"} {
		n := New(st, id, "h", "v", log)
		go n.Singleton(ctx, "retention", func(ctx context.Context) {
			mu.Lock()
			running[id] = true
			mu.Unlock()
			<-ctx.Done()
		})
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(running)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(running) != 1 {
		t.Fatalf("one server runs the job: %v", running)
	}
}

func TestBusSkipsItsOwnMessagesAndRoutesByKind(t *testing.T) {
	st := newMem()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := New(st, "a", "h", "v", log)
	bus := NewBus(a, st, log)
	var got []string
	bus.DeliverTo(func(kind string, _ any) { got = append(got, "local:"+kind) })
	bus.Handle("collector.run_soon", func(json.RawMessage) { got = append(got, "handled") })
	bus.dispatch(`{"from":"a","kind":"incident.opened","payload":{}}`)
	bus.dispatch(`{"from":"b","kind":"incident.opened","payload":{}}`)
	bus.dispatch(`{"from":"b","kind":"collector.run_soon","payload":{}}`)
	if strings.Join(got, ",") != "local:incident.opened,handled" {
		t.Fatalf("dispatch %v", got)
	}
	// Alone, nothing is sent.
	bus.Send("x", 1)
	if len(bus.queue) != 0 {
		t.Fatal("a server alone sends nothing")
	}
}
