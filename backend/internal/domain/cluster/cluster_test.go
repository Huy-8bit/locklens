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

	"github.com/locklens/locklens/internal/lib/obs"
	"github.com/locklens/locklens/internal/repository"
)

// memStore holds leases and heartbeats in memory, on a fake clock.
type memStore struct {
	repository.ClusterRepository
	mu      sync.Mutex
	now     time.Time
	beats   map[string]time.Time
	servers map[string]repository.Server
	leases  map[string]repository.Lease
	notices []string
	// batchCalls counts HoldLeases and ReleaseLeases statements.
	batchCalls int
	epoch      int64
	weights    map[int64]repository.TargetWeight
}

func newMem() *memStore {
	return &memStore{now: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC), beats: map[string]time.Time{}, servers: map[string]repository.Server{}, leases: map[string]repository.Lease{}}
}

func (m *memStore) Heartbeat(_ context.Context, sv repository.Server) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.beats[sv.ID] = m.now
	m.servers[sv.ID] = sv
	return nil
}

func (m *memStore) Servers(_ context.Context, ttl time.Duration) ([]repository.Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []repository.Server
	for id, at := range m.beats {
		roles := m.servers[id].Roles
		if len(roles) == 0 {
			roles = repository.AllRoles // a server that predates roles
		}
		sv := m.servers[id]
		out = append(out, repository.Server{ID: id, HeartbeatAt: at, Alive: !at.Before(m.now.Add(-ttl)), Roles: roles,
			Capacity: sv.Capacity, Load: sv.Load, MaxTargets: sv.MaxTargets})
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
		m.epoch++
		l = repository.Lease{Name: name, Holder: holder, AcquiredAt: m.now, Epoch: m.epoch}
	}
	l.ExpiresAt = m.now.Add(ttl)
	m.leases[name] = l
	return true, l.AcquiredAt, nil
}

func (m *memStore) HoldLeases(ctx context.Context, names []string, holder string, ttl time.Duration) (map[string]int64, error) {
	m.mu.Lock()
	m.batchCalls++
	m.mu.Unlock()
	out := map[string]int64{}
	for _, name := range names {
		if ok, _, _ := m.HoldLease(ctx, name, holder, ttl); ok {
			m.mu.Lock()
			out[name] = m.leases[name].Epoch
			m.mu.Unlock()
		}
	}
	return out, nil
}

func (m *memStore) TargetWeights(context.Context) (map[int64]repository.TargetWeight, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[int64]repository.TargetWeight{}
	for id, w := range m.weights {
		out[id] = w
	}
	return out, nil
}

func (m *memStore) SaveTargetWeights(_ context.Context, ids []int64, busy []float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.weights == nil {
		m.weights = map[int64]repository.TargetWeight{}
	}
	for i, id := range ids {
		w := m.weights[id]
		v := busy[i]
		w.BusyPerMin = &v
		m.weights[id] = w
	}
	return nil
}

func (m *memStore) clock() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now
}

func (m *memStore) ReleaseLeases(ctx context.Context, names []string, holder string) error {
	m.mu.Lock()
	m.batchCalls++
	m.mu.Unlock()
	for _, name := range names {
		_ = m.ReleaseLease(ctx, name, holder)
	}
	return nil
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
	a, b := New(Repositories{ClusterRepository: st}, Self{ID: "a", Hostname: "h", Version: "v"}, log, nil).(*node), New(Repositories{ClusterRepository: st}, Self{ID: "b", Hostname: "h", Version: "v"}, log, nil).(*node)
	a.now, b.now = st.clock, st.clock
	targets := []int64{1, 2, 3, 4, 5}

	// Alone, a server collects everything.
	a.beat(ctx)
	if got, _ := a.Assign(ctx, targets); len(got) != 5 {
		t.Fatalf("alone: %v", got)
	}
	// A second server joins: a gives up targets beyond its share, b takes
	// them on its next sync, a few per sync, until neither is far from its
	// share.
	b.beat(ctx)
	a.beat(ctx)
	var ga, gb map[int64]bool
	for range 5 {
		ga, _ = a.Assign(ctx, targets)
		gb, _ = b.Assign(ctx, targets)
	}
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

func TestAssignCostsAConstantNumberOfStatements(t *testing.T) {
	ctx := context.Background()
	st := newMem()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, b := New(Repositories{ClusterRepository: st}, Self{ID: "a", Hostname: "h", Version: "v"}, log, nil).(*node), New(Repositories{ClusterRepository: st}, Self{ID: "b", Hostname: "h", Version: "v"}, log, nil).(*node)
	targets := make([]int64, 300)
	for i := range targets {
		targets[i] = int64(i + 1)
	}
	a.beat(ctx)
	if got, _ := a.Assign(ctx, targets); len(got) != 300 {
		t.Fatalf("alone: %d", len(got))
	}
	if st.batchCalls != 1 {
		t.Fatalf("taking 300 leases took %d statements, want 1", st.batchCalls)
	}
	// b joins: a renews and releases (5% of the targets per sync) in two
	// statements.
	b.beat(ctx)
	a.beat(ctx)
	st.batchCalls = 0
	if got, _ := a.Assign(ctx, targets); len(got) != 285 {
		t.Fatalf("a after b joined: %d", len(got))
	}
	if st.batchCalls != 2 {
		t.Fatalf("renew + release took %d statements, want 2", st.batchCalls)
	}
	if got, _ := b.Assign(ctx, targets); len(got) != 15 {
		t.Fatalf("b: %d", len(got))
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
		n := New(Repositories{ClusterRepository: st}, Self{ID: id, Hostname: "h", Version: "v"}, log, nil)
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
	a := New(Repositories{ClusterRepository: st}, Self{ID: "a", Hostname: "h", Version: "v"}, log, nil)
	var got []string
	bus := NewBus(a, Repositories{ClusterRepository: st}, publishFunc(func(kind string, _ any) { got = append(got, "local:"+kind) }), nil, log).(*bus)
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

// publishFunc is a publisher that calls a function.
type publishFunc func(kind string, payload any)

func (f publishFunc) Publish(kind string, payload any) { f(kind, payload) }

func TestCollectedEventsAreCoalescedPerTarget(t *testing.T) {
	ctx := context.Background()
	st := newMem()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, b := New(Repositories{ClusterRepository: st}, Self{ID: "a", Hostname: "h", Version: "v"}, log, nil).(*node), New(Repositories{ClusterRepository: st}, Self{ID: "b", Hostname: "h", Version: "v"}, log, nil).(*node)
	b.beat(ctx)
	a.beat(ctx) // a is not alone: it sends
	bs := NewBus(a, Repositories{ClusterRepository: st}, publishFunc(func(string, any) {}), nil, log).(*bus)
	clock := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	bs.now = func() time.Time { return clock }
	send := func(target int64, collector string, ok bool) {
		bs.Send("target.collected", map[string]any{"target_id": target, "collector": collector, "at": clock, "ok": ok})
	}
	drain := func() []collectedEvent {
		var out []collectedEvent
		for len(bs.queue) > 0 {
			e := <-bs.queue
			var ev collectedEvent
			_ = json.Unmarshal(e.Payload, &ev)
			out = append(out, ev)
		}
		return out
	}

	// The first event of a target leaves at once; the next ones within a
	// second are merged into one.
	send(1, "activity", true)
	send(1, "database", true)
	send(1, "statements", false)
	send(2, "activity", true)
	if got := drain(); len(got) != 2 || got[0].TargetID != 1 || got[1].TargetID != 2 {
		t.Fatalf("first events: %+v", got)
	}
	bs.flushCoalesced()
	if got := drain(); len(got) != 0 {
		t.Fatalf("merged event sent before a second passed: %+v", got)
	}
	clock = clock.Add(time.Second)
	bs.flushCoalesced()
	got := drain()
	if len(got) != 1 || got[0].TargetID != 1 || strings.Join(got[0].Collectors, ",") != "database,statements" || got[0].OK {
		t.Fatalf("merged event: %+v", got)
	}
	// 100 events of one target in one second: at most two messages.
	for range 100 {
		send(3, "load", true)
	}
	clock = clock.Add(time.Second)
	bs.flushCoalesced()
	if got := drain(); len(got) != 2 {
		t.Fatalf("100 events in a second became %d messages, want 2", len(got))
	}
	// Other kinds are not coalesced.
	bs.Send("incident.updated", map[string]any{"id": 1})
	bs.Send("incident.updated", map[string]any{"id": 1})
	if len(bs.queue) != 2 {
		t.Fatalf("incident events: %d queued", len(bs.queue))
	}
}

func TestTargetsAreSharedAmongCollectorsOnly(t *testing.T) {
	ctx := context.Background()
	st := newMem()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := New(Repositories{ClusterRepository: st}, Self{ID: "a", Roles: []string{repository.RoleCollector}}, log, nil).(*node)
	b := New(Repositories{ClusterRepository: st}, Self{ID: "b", Roles: []string{repository.RoleCollector, repository.RoleJobs}}, log, nil).(*node)
	api := New(Repositories{ClusterRepository: st}, Self{ID: "api", Roles: []string{repository.RoleAPI}}, log, nil).(*node)
	targets := []int64{1, 2, 3, 4, 5, 6}
	for _, n := range []*node{api, a, b, a, b, api} {
		n.beat(ctx)
	}
	// Two collectors share six targets 3/3: the API server does not count.
	ga, _ := a.Assign(ctx, targets)
	gb, _ := b.Assign(ctx, targets)
	if len(ga) != 3 || len(gb) != 3 {
		t.Fatalf("share among collectors: a %d b %d", len(ga), len(gb))
	}
	if api.Alone() {
		t.Fatal("the API server is not alone: collectors publish live views for it")
	}
}

func TestOrphanedTargetsAreRescued(t *testing.T) {
	ctx := context.Background()
	st := newMem()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// An old server (no roles: counted as a collector) never collects, as an
	// API-only server would look to it; b alone thinks it must leave half the
	// targets to it.
	a := New(Repositories{ClusterRepository: st}, Self{ID: "a"}, log, nil).(*node)
	b := New(Repositories{ClusterRepository: st}, Self{ID: "b"}, log, nil).(*node)
	targets := []int64{1, 2, 3, 4}
	a.beat(ctx)
	b.beat(ctx)
	for sync := 1; sync <= orphanSyncs; sync++ {
		if got, _ := b.Assign(ctx, targets); len(got) != 2 {
			t.Fatalf("sync %d: b holds %d, want its share of 2", sync, len(got))
		}
	}
	// After orphanSyncs syncs without a collector, b takes the rest too.
	if got, _ := b.Assign(ctx, targets); len(got) != 4 {
		t.Fatalf("orphans not rescued: b holds %d", len(got))
	}
}

func TestNoJobsServerIsReported(t *testing.T) {
	ctx := context.Background()
	st := newMem()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := obs.NewMetrics()
	c := New(Repositories{ClusterRepository: st}, Self{ID: "c", Roles: []string{repository.RoleCollector}}, log, m).(*node)
	c.beat(ctx)
	if m.JobsMissing.Total() != 1 {
		t.Fatal("no jobs server: locklens_cluster_jobs_missing must be 1")
	}
	j := New(Repositories{ClusterRepository: st}, Self{ID: "j", Roles: []string{repository.RoleJobs}}, log, nil).(*node)
	j.beat(ctx)
	c.beat(ctx)
	if m.JobsMissing.Total() != 0 {
		t.Fatal("a jobs server is alive")
	}
}
