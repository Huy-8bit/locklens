// Package collector runs the server's background collection: one worker per
// enabled target, each running its collectors sequentially (so collectors of
// one target never overlap), with a global limit on concurrent collections
// across targets. Failures of one target never block another.
package collector

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/locklens/locklens/internal/domain/cluster"
	"github.com/locklens/locklens/internal/domain/events"
	"github.com/locklens/locklens/internal/domain/incidents"
	"github.com/locklens/locklens/internal/domain/live"
	"github.com/locklens/locklens/internal/domain/settings"
	"github.com/locklens/locklens/internal/lib/obs"
	"github.com/locklens/locklens/internal/lib/secret"
	"github.com/locklens/locklens/internal/repository"
)

// MessageRunSoon asks the server that collects a target to run a collector
// now ({"target_id", "collector"}).
const MessageRunSoon = "collector.run_soon"

// liveEvery is how often a server publishes the live view of the targets
// it collects for the other servers.
const liveEvery = 5 * time.Second

// Config configures the manager.
type Config struct {
	MaxConcurrent int
	SyncInterval  time.Duration
	Version       string
	// StatementsInterval is the minimum pg_stat_statements interval (default 60 s).
	StatementsInterval time.Duration
	// LoadInterval is the active session sampling interval; 0 turns
	// sampling off.
	LoadInterval time.Duration
	// BurstDuration is how long a target is collected faster after Burst;
	// 0 turns adaptive sampling off.
	BurstDuration time.Duration
	// BudgetPercent is the share of its interval a collector may spend
	// before it is slowed down (default 10).
	BudgetPercent int
}

// Deps are the manager's collaborators.
type Deps struct {
	Store  Repositories
	Box    *secret.Box
	Writer *Writer
	// Recorder analyses each activity collection; nil: no analysis.
	Recorder incidents.Recorder
	Events   events.Publisher
	Settings settings.Service
	// Cluster and Bus are set when several servers may share the store:
	// the manager collects only the targets assigned to this server,
	// publishes their live views, and forwards requests about other
	// targets. Nil: this server collects every target.
	Cluster cluster.Node
	Bus     cluster.Bus
	Metrics *obs.Metrics
	Log     *slog.Logger
}

// Manager runs the collectors of the targets this server collects and holds their live views.
type Manager interface {
	// Burst collects a target faster for the configured burst duration (from
	// now), for example while it has an open incident. Calling it again extends
	// the burst. It does nothing when adaptive sampling is off.
	Burst(targetID int64, reason string)
	// RunSoon asks a target's worker to run a collector now instead of at its
	// next turn; with a cluster, the server that collects it is asked. False
	// when no worker could be asked.
	RunSoon(targetID int64, collector string) bool
	// RunSoonHere is RunSoon for a target this server collects.
	RunSoonHere(targetID int64, collector string) bool
	// Reload asks the manager to re-read targets now (after a change via the API).
	Reload()
	// live.View returns the live view of a target: this server's when it collects
	// the target, otherwise the one its collecting server published (when it is
	// recent), otherwise the last one this server had.
	View(ctx context.Context, id int64) (live.View, bool)
	// Run synchronises workers with the stored targets until ctx is cancelled,
	// then stops every worker.
	Run(ctx context.Context)
}

var _ Manager = (*manager)(nil)

// manager owns all target workers.
type manager struct {
	store     Repositories
	box       *secret.Box
	writer    *Writer
	recorder  incidents.Recorder
	events    events.Publisher
	settings  settings.Service
	metrics   *obs.Metrics
	log       *slog.Logger
	version   string
	syncEvery time.Duration
	stmtEvery time.Duration
	loadEvery time.Duration
	burstDur  time.Duration
	budget    float64

	sem       chan struct{}
	slotsBusy atomic.Int64
	reload    chan struct{}

	cluster cluster.Node
	bus     cluster.Bus
	remote  remoteViews

	mu      sync.Mutex
	workers map[int64]*running
	states  map[int64]*State
}

type running struct {
	cancel    context.CancelFunc
	done      chan struct{}
	updatedAt time.Time
}

// NewManager builds a manager.
func NewManager(d Deps, cfg Config) Manager {
	if cfg.MaxConcurrent < 1 {
		cfg.MaxConcurrent = 4
	}
	if cfg.SyncInterval <= 0 {
		cfg.SyncInterval = 15 * time.Second
	}
	if cfg.StatementsInterval <= 0 {
		cfg.StatementsInterval = 15 * time.Second
	}
	if cfg.BudgetPercent <= 0 {
		cfg.BudgetPercent = 10
	}
	return &manager{store: d.Store, box: d.Box, writer: d.Writer, recorder: d.Recorder, events: d.Events, settings: d.Settings,
		cluster: d.Cluster, bus: d.Bus, metrics: d.Metrics, log: d.Log, version: cfg.Version, syncEvery: cfg.SyncInterval, stmtEvery: cfg.StatementsInterval, loadEvery: cfg.LoadInterval,
		burstDur: cfg.BurstDuration, budget: float64(cfg.BudgetPercent) / 100,
		sem: make(chan struct{}, cfg.MaxConcurrent), reload: make(chan struct{}, 1),
		workers: map[int64]*running{}, states: map[int64]*State{}}
}

// Burst collects a target faster for the configured burst duration (from
// now), for example while it has an open incident. Calling it again extends
// the burst. It does nothing when adaptive sampling is off.
func (m *manager) Burst(targetID int64, reason string) {
	if m.burstDur <= 0 {
		return
	}
	if s, ok := m.stateOf(targetID); ok {
		s.setBurst(time.Now().Add(m.burstDur), reason)
	}
}

// RunSoon asks a target's worker to run a collector now instead of at its
// next turn; with a cluster, the server that collects it is asked. False
// when no worker could be asked.
func (m *manager) RunSoon(targetID int64, collector string) bool {
	if m.RunSoonHere(targetID, collector) {
		return true
	}
	if m.bus != nil && m.cluster != nil && !m.cluster.Alone() {
		m.bus.Send(MessageRunSoon, map[string]any{"target_id": targetID, "collector": collector})
		return true
	}
	return false
}

// RunSoonHere is RunSoon for a target this server collects.
func (m *manager) RunSoonHere(targetID int64, collector string) bool {
	m.mu.Lock()
	_, running := m.workers[targetID]
	s := m.states[targetID]
	m.mu.Unlock()
	if !running || s == nil {
		return false
	}
	s.requestRun(collector)
	return true
}

// Reload asks the manager to re-read targets now (after a change via the API).
func (m *manager) Reload() {
	select {
	case m.reload <- struct{}{}:
	default:
	}
}

// stateOf returns the in-memory state of a target, if any.
func (m *manager) stateOf(id int64) (*State, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.states[id]
	return s, ok
}

// live.View returns the live view of a target: this server's when it collects
// the target, otherwise the one its collecting server published (when it is
// recent), otherwise the last one this server had.
func (m *manager) View(ctx context.Context, id int64) (live.View, bool) {
	m.mu.Lock()
	_, running := m.workers[id]
	s := m.states[id]
	m.mu.Unlock()
	if running && s != nil {
		return s.Snapshot(), true
	}
	if m.cluster != nil && !m.cluster.Alone() {
		if v, ok := m.remote.get(ctx, m.store, id); ok {
			return v, true
		}
	}
	if s != nil {
		return s.Snapshot(), true
	}
	return live.View{}, false
}

// Run synchronises workers with the stored targets until ctx is cancelled,
// then stops every worker.
func (m *manager) Run(ctx context.Context) {
	t := time.NewTicker(m.syncEvery)
	defer t.Stop()
	if m.cluster != nil {
		go m.publishLive(ctx)
	}
	m.sync(ctx)
	for {
		select {
		case <-ctx.Done():
			m.stopAll()
			return
		case <-t.C:
		case <-m.reload:
		}
		m.sync(ctx)
	}
}

func (m *manager) sync(ctx context.Context) {
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	targets, err := m.store.ListTargets(lctx)
	cancel()
	if err != nil {
		m.log.Warn("list targets failed; keeping current workers", "error", err.Error())
		return
	}
	wanted := map[int64]repository.Target{}
	for _, t := range targets {
		if t.Enabled {
			wanted[t.ID] = t
		}
	}
	if m.cluster != nil {
		ids := make([]int64, 0, len(wanted))
		for id := range wanted {
			ids = append(ids, id)
		}
		actx, cancel := context.WithTimeout(ctx, 10*time.Second)
		owned, err := m.cluster.Assign(actx, ids)
		cancel()
		if err != nil {
			m.log.Warn("cluster assignment failed; keeping current workers", "error", err.Error())
			return
		}
		for id := range wanted {
			if !owned[id] {
				delete(wanted, id)
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	updated := map[int64]bool{}
	for id, r := range m.workers {
		t, ok := wanted[id]
		if !ok || !t.UpdatedAt.Equal(r.updatedAt) {
			r.cancel()
			<-r.done
			delete(m.workers, id)
			if ok {
				updated[id] = true
			} else {
				m.metrics.CollectFreshness.Delete(strconv.FormatInt(id, 10))
				if m.recorder != nil {
					m.recorder.Forget(id)
				}
			}
		}
	}
	for id, t := range wanted {
		if _, ok := m.workers[id]; ok {
			continue
		}
		// A target this server starts collecting (at start-up or taken
		// over from another server): attach its open incidents.
		if m.recorder != nil && !updated[id] {
			actx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := m.recorder.Adopt(actx, id); err != nil {
				m.log.Warn("adopting open incidents failed", "target_id", id, "error", err.Error())
			}
			cancel()
		}
		st, ok := m.states[id]
		if !ok {
			st = newState()
			m.states[id] = st
		}
		wctx, cancel := context.WithCancel(ctx)
		r := &running{cancel: cancel, done: make(chan struct{}), updatedAt: t.UpdatedAt}
		m.workers[id] = r
		w := newWorker(m, t, st)
		go func() {
			defer close(r.done)
			w.run(wctx)
		}()
	}
	// Forget state of targets that no longer exist at all.
	known := map[int64]bool{}
	for _, t := range targets {
		known[t.ID] = true
	}
	for id := range m.states {
		if !known[id] {
			delete(m.states, id)
		}
	}
	m.metrics.ActiveTargets.Set(float64(len(m.workers)))
}

func (m *manager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.workers {
		r.cancel()
		<-r.done
		delete(m.workers, id)
	}
	m.metrics.ActiveTargets.Set(0)
}

// publishLive publishes the live views of the targets this server collects
// while other servers share the store, so they can answer API requests.
func (m *manager) publishLive(ctx context.Context) {
	t := time.NewTicker(liveEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if m.cluster.Alone() {
			continue
		}
		m.mu.Lock()
		views := map[int64]live.View{}
		for id := range m.workers {
			if s := m.states[id]; s != nil {
				views[id] = s.Snapshot()
			}
		}
		m.mu.Unlock()
		for id, v := range views {
			// Statement collections and load buckets are not read from the
			// live view by the API; they are large.
			v.Statements, v.Load = nil, nil
			raw, err := json.Marshal(v)
			if err != nil {
				continue
			}
			pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if err := m.store.SaveLiveState(pctx, id, m.cluster.ID(), time.Now(), raw); err != nil && ctx.Err() == nil {
				m.log.Warn("publishing live view failed", "target_id", id, "error", err.Error())
			}
			cancel()
		}
	}
}

// remoteViews caches the live views other servers published.
type remoteViews struct {
	mu    sync.Mutex
	cache map[int64]remoteView
}

type remoteView struct {
	fetched time.Time
	view    live.View
	ok      bool
}

// get returns a published view younger than a minute (cached two seconds).
func (r *remoteViews) get(ctx context.Context, store Repositories, id int64) (live.View, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.cache[id]; ok && time.Since(c.fetched) < 2*time.Second {
		return c.view, c.ok
	}
	if r.cache == nil {
		r.cache = map[int64]remoteView{}
	}
	c := remoteView{fetched: time.Now()}
	if _, at, raw, err := store.LiveState(ctx, id); err == nil && time.Since(at) < time.Minute {
		c.ok = json.Unmarshal(raw, &c.view) == nil
	}
	r.cache[id] = c
	return c.view, c.ok
}
