// Package collector runs the server's background collection: one worker per
// enabled target, each running its collectors sequentially (so collectors of
// one target never overlap), with a global limit on concurrent collections
// across targets. Failures of one target never block another.
package collector

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/locklens/locklens/internal/domain/cluster"
	"github.com/locklens/locklens/internal/domain/events"
	"github.com/locklens/locklens/internal/domain/incidents"
	"github.com/locklens/locklens/internal/domain/live"
	"github.com/locklens/locklens/internal/domain/observe"
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
	// MaxConcurrentLoad caps active session samples running at once (their
	// own slots, so they never queue behind the other collectors).
	MaxConcurrentLoad int
	SyncInterval      time.Duration
	Version           string
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
	// ViewOnly is a server without the collector role: it runs no workers
	// and reads every live view from the ones collectors publish.
	ViewOnly bool
	// ExportTopStatements is how many statements of the latest collection a
	// published live view keeps (the Prometheus export of API servers).
	ExportTopStatements int
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
	// Collecting returns the live views of the targets this server
	// collects (in a cluster, each target is collected by one server).
	Collecting() map[int64]live.View
	// Views is View for many targets (fleet pages): targets collected
	// elsewhere are read from the store in one statement. Targets without a
	// view are absent.
	Views(ctx context.Context, ids []int64) map[int64]live.View
	// ExportViews returns the views of the instances this server exports:
	// the ones it collects, or on a server without the collector role every
	// one of ids, as published.
	ExportViews(ctx context.Context, ids []int64) map[int64]live.View
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
	// loadSem bounds load samples separately: they run every few seconds per
	// target and take milliseconds, so sharing sem would make them the bulk
	// of its queue.
	loadSem  chan struct{}
	loadBusy atomic.Int64
	reload   chan struct{}

	cluster cluster.Node
	bus     cluster.Bus
	remote  remoteViews
	status  *statusWriter

	viewOnly  bool
	exportTop int
	// published holds a hash of each target's last published live view.
	published map[int64]uint64

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
	if cfg.MaxConcurrentLoad < 1 {
		cfg.MaxConcurrentLoad = 4
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
		sem: make(chan struct{}, cfg.MaxConcurrent), loadSem: make(chan struct{}, cfg.MaxConcurrentLoad), reload: make(chan struct{}, 1),
		workers: map[int64]*running{}, states: map[int64]*State{}, status: newStatusWriter(),
		viewOnly: cfg.ViewOnly, exportTop: cfg.ExportTopStatements, published: map[int64]uint64{}}
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
	if m.viewOnly || m.cluster != nil && !m.cluster.Alone() {
		from := time.Now()
		v, ok := m.remote.get(ctx, m.store, id)
		m.metrics.LiveRemoteRead.Observe(time.Since(from).Seconds())
		if ok {
			return v, true
		}
	}
	if s != nil {
		return s.Snapshot(), true
	}
	return live.View{}, false
}

// Views is View for many targets (fleet pages): targets collected elsewhere
// are read from the store in one statement.
func (m *manager) Views(ctx context.Context, ids []int64) map[int64]live.View {
	out := make(map[int64]live.View, len(ids))
	var others []int64
	m.mu.Lock()
	stale := map[int64]*State{}
	for _, id := range ids {
		_, running := m.workers[id]
		s := m.states[id]
		switch {
		case running && s != nil:
			out[id] = s.Snapshot()
		default:
			others = append(others, id)
			if s != nil {
				stale[id] = s
			}
		}
	}
	m.mu.Unlock()
	if len(others) > 0 && (m.viewOnly || m.cluster != nil && !m.cluster.Alone()) {
		from := time.Now()
		for id, v := range m.remote.getMany(ctx, m.store, others) {
			out[id] = v
		}
		m.metrics.LiveRemoteRead.Observe(time.Since(from).Seconds())
	}
	// As View: otherwise the last view this server had.
	for id, s := range stale {
		if _, ok := out[id]; !ok {
			out[id] = s.Snapshot()
		}
	}
	return out
}

// ExportViews returns the views of the instances this server exports
// (/metrics/targets, OTLP): the ones it collects, or, on a server without the
// collector role, every one of ids as published by the collectors.
func (m *manager) ExportViews(ctx context.Context, ids []int64) map[int64]live.View {
	if m.viewOnly {
		return m.Views(ctx, ids)
	}
	return m.Collecting()
}

// Run synchronises workers with the stored targets until ctx is cancelled,
// then stops every worker.
func (m *manager) Run(ctx context.Context) {
	if m.viewOnly {
		<-ctx.Done()
		return
	}
	t := time.NewTicker(m.syncEvery)
	defer t.Stop()
	if m.cluster != nil {
		go m.publishLive(ctx)
	}
	go m.runStatus(ctx)
	m.sync(ctx)
	for {
		select {
		case <-ctx.Done():
			m.stopAll()
			// The workers' last statuses (for example "stopped collecting").
			m.flushStatus(context.Background())
			return
		case <-t.C:
		case <-m.reload:
		}
		m.sync(ctx)
	}
}

func (m *manager) sync(ctx context.Context) {
	defer func(from time.Time) { m.metrics.ManagerSync.Observe(time.Since(from).Seconds()) }(time.Now())
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
		assignFrom := time.Now()
		owned, err := m.cluster.Assign(actx, ids)
		m.metrics.ClusterAssign.Observe(time.Since(assignFrom).Seconds())
		cancel()
		if err != nil {
			m.metrics.ClusterAssignErr.Inc()
			m.log.Warn("cluster assignment failed; keeping current workers", "error", err.Error())
			return
		}
		for id := range wanted {
			if !owned[id] {
				delete(wanted, id)
			}
		}
	}
	// Decide under the lock, but stop workers (which waits for a running
	// collection) and adopt incidents (store reads) without it: View,
	// Collecting and the live view publisher must not wait for a takeover of
	// many targets.
	m.mu.Lock()
	var stopping []*running
	updated := map[int64]bool{}
	var forgotten []int64
	for id, r := range m.workers {
		t, ok := wanted[id]
		if !ok || !t.UpdatedAt.Equal(r.updatedAt) {
			r.cancel()
			stopping = append(stopping, r)
			delete(m.workers, id)
			if ok {
				updated[id] = true
			} else {
				forgotten = append(forgotten, id)
			}
		}
	}
	var starts []repository.Target
	for id, t := range wanted {
		if _, ok := m.workers[id]; !ok {
			starts = append(starts, t)
		}
	}
	m.mu.Unlock()

	for _, r := range stopping {
		<-r.done
	}
	for _, id := range forgotten {
		m.status.forget(id)
		m.metrics.CollectFreshness.Delete(strconv.FormatInt(id, 10))
		if m.recorder != nil {
			m.recorder.Forget(id)
		}
	}
	for _, t := range starts {
		// A target this server starts collecting (at start-up or taken over
		// from another server): attach its open incidents.
		if m.recorder != nil && !updated[t.ID] {
			actx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := m.recorder.Adopt(actx, t.ID); err != nil {
				m.log.Warn("adopting open incidents failed", "target_id", t.ID, "error", err.Error())
			}
			cancel()
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range starts {
		if _, ok := m.workers[t.ID]; ok {
			continue
		}
		st, ok := m.states[t.ID]
		if !ok {
			st = newState()
			m.states[t.ID] = st
		}
		wctx, cancel := context.WithCancel(ctx)
		r := &running{cancel: cancel, done: make(chan struct{}), updatedAt: t.UpdatedAt}
		m.workers[t.ID] = r
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
	stopping := make([]*running, 0, len(m.workers))
	for id, r := range m.workers {
		r.cancel()
		stopping = append(stopping, r)
		delete(m.workers, id)
	}
	m.mu.Unlock()
	for _, r := range stopping {
		<-r.done
	}
	m.metrics.ActiveTargets.Set(0)
}

func (m *manager) Collecting() map[int64]live.View {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[int64]live.View, len(m.workers))
	for id := range m.workers {
		if s := m.states[id]; s != nil {
			out[id] = s.Snapshot()
		}
	}
	return out
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
		m.publishOnce(ctx)
	}
}

// publishOnce writes the changed live views in one statement and marks the
// unchanged ones as current in another.
func (m *manager) publishOnce(ctx context.Context) {
	m.mu.Lock()
	views := map[int64]live.View{}
	for id := range m.workers {
		if s := m.states[id]; s != nil {
			views[id] = s.Snapshot()
		}
	}
	m.mu.Unlock()
	if len(views) == 0 {
		return
	}
	from := time.Now()
	var changed, unchanged, changedEpochs, unchangedEpochs []int64
	var raws [][]byte
	hashes := map[int64]uint64{}
	for id, v := range views {
		f, ok := m.cluster.Fence(id)
		if !ok {
			continue // lease lost or about to expire: the new holder publishes
		}
		raw, err := json.Marshal(publishable(v, m.exportTop))
		if err != nil {
			continue
		}
		m.metrics.LiveViewBytes.Observe(float64(len(raw)))
		h := fnv.New64a()
		h.Write(raw)
		hashes[id] = h.Sum64()
		if prev, ok := m.published[id]; ok && prev == hashes[id] {
			unchanged = append(unchanged, id)
			unchangedEpochs = append(unchangedEpochs, f.Epoch)
			continue
		}
		changed = append(changed, id)
		changedEpochs = append(changedEpochs, f.Epoch)
		raws = append(raws, raw)
	}
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	now := time.Now()
	written, err := m.store.SaveLiveStates(pctx, m.cluster.ID(), now, changed, raws, changedEpochs)
	if err != nil {
		if ctx.Err() == nil {
			m.metrics.LivePublishErrors.Inc()
			m.log.Warn("publishing live views failed", "targets", len(changed), "error", err.Error())
		}
		clear(m.published) // write everything at the next tick
		return
	}
	ok := idSet(written)
	for i, id := range changed {
		if !ok[id] {
			m.leaseLost("live view", id)
			continue
		}
		m.published[id] = hashes[id]
		m.metrics.LivePublishBytes.Add(float64(len(raws[i])))
	}
	touched, err := m.store.TouchLiveStates(pctx, m.cluster.ID(), now, unchanged, unchangedEpochs)
	if err != nil && ctx.Err() == nil {
		m.metrics.LivePublishErrors.Inc()
		m.log.Warn("marking live views current failed", "targets", len(unchanged), "error", err.Error())
		clear(m.published)
	} else if err == nil {
		ok = idSet(touched)
		for _, id := range unchanged {
			if !ok[id] {
				// Lease lost, or the row is gone (store restarted: live_state
				// is unlogged): write it again at the next tick if still held.
				delete(m.published, id)
			}
		}
	}
	for id := range m.published {
		if _, ok := views[id]; !ok {
			delete(m.published, id) // no longer collected here
		}
	}
	m.metrics.LivePublish.Observe(time.Since(from).Seconds())
}

func idSet(ids []int64) map[int64]bool {
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// leaseLost counts and logs a write about a target that fencing rejected:
// another server took the target over (this one stalled past its lease).
func (m *manager) leaseLost(write string, targetID int64) {
	m.metrics.FencedWrites.Inc(write)
	m.log.Warn("write rejected: this server no longer holds the target's lease", "write", write, "target_id", targetID)
}

// publishable is the part of a live view other servers read: the API reads
// the latest two points of each series (latest value and trend), and the
// Prometheus export the latest point, the totals, and the busiest statements
// of the latest collection. The point history (for rules and live charts
// of the collecting server), active session samples, and older statement
// collections stay in memory.
func publishable(v live.View, topStatements int) live.View {
	if len(v.Points) > 0 {
		pts := make(map[string][]live.TimedValue, len(v.Points))
		for k, h := range v.Points {
			pts[k] = h[max(0, len(h)-2):]
		}
		v.Points = pts
	}
	v.Load = nil
	if topStatements > 0 && len(v.Statements) > 0 {
		last := v.Statements[len(v.Statements)-1]
		last.Deltas = last.TopByTime(topStatements)
		v.Statements = []observe.StatementsCollection{last}
	} else {
		v.Statements = nil
	}
	return v
}

// remoteViews caches the live views other servers published. The lock only
// guards the map: store reads run without it, so concurrent API requests for
// different targets do not queue behind one another.
type remoteViews struct {
	mu    sync.Mutex
	cache map[int64]remoteView
}

type remoteView struct {
	fetched time.Time
	view    live.View
	ok      bool
}

// remoteFresh is how long a fetched view is reused; remoteMaxAge is how old a
// published view may be and still be shown.
const (
	remoteFresh  = 2 * time.Second
	remoteMaxAge = time.Minute
)

func (r *remoteViews) cached(id int64, now time.Time) (remoteView, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.cache[id]
	return c, ok && now.Sub(c.fetched) < remoteFresh
}

func (r *remoteViews) put(id int64, c remoteView) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache == nil {
		r.cache = map[int64]remoteView{}
	}
	r.cache[id] = c
}

// get returns a published view younger than a minute (cached two seconds).
func (r *remoteViews) get(ctx context.Context, store Repositories, id int64) (live.View, bool) {
	now := time.Now()
	if c, ok := r.cached(id, now); ok {
		return c.view, c.ok
	}
	c := remoteView{fetched: now}
	if _, at, raw, err := store.LiveState(ctx, id); err == nil && now.Sub(at) < remoteMaxAge {
		c.ok = json.Unmarshal(raw, &c.view) == nil
	}
	r.put(id, c)
	return c.view, c.ok
}

// getMany is get for many targets with one store read for those not cached.
func (r *remoteViews) getMany(ctx context.Context, store Repositories, ids []int64) map[int64]live.View {
	now := time.Now()
	out := make(map[int64]live.View, len(ids))
	var missing []int64
	for _, id := range ids {
		if c, ok := r.cached(id, now); ok {
			if c.ok {
				out[id] = c.view
			}
			continue
		}
		missing = append(missing, id)
	}
	if len(missing) == 0 {
		return out
	}
	rows, err := store.LiveStates(ctx, missing)
	if err != nil {
		return out // like get: unknown views are simply absent
	}
	found := make(map[int64]remoteView, len(rows))
	for _, row := range rows {
		c := remoteView{fetched: now}
		if now.Sub(row.At) < remoteMaxAge {
			c.ok = json.Unmarshal(row.View, &c.view) == nil
		}
		found[row.TargetID] = c
	}
	for _, id := range missing {
		c, ok := found[id]
		if !ok {
			c = remoteView{fetched: now}
		}
		r.put(id, c)
		if c.ok {
			out[id] = c.view
		}
	}
	return out
}
