// Package collector runs the server's background collection: one worker per
// enabled target, each running its collectors sequentially (so collectors of
// one target never overlap), with a global limit on concurrent collections
// across targets. Failures of one target never block another.
package collector

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/locklens/locklens/internal/lib/obs"
	"github.com/locklens/locklens/internal/lib/secret"
	"github.com/locklens/locklens/internal/repository"
)

// Analyzer evaluates rules and records incidents after each activity
// collection. It must return quickly; ctx carries a timeout.
type Analyzer interface {
	Analyze(ctx context.Context, t repository.Target, v View)
}

// EventSink receives events for server-sent events.
type EventSink interface {
	Publish(kind string, payload any)
}

// Settings provides thresholds that collectors need.
type Settings interface {
	LongTransactionThreshold() time.Duration
}

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
}

// Manager owns all target workers.
type Manager struct {
	store     *repository.Store
	box       *secret.Box
	writer    *Writer
	analyzer  Analyzer
	events    EventSink
	settings  Settings
	metrics   *obs.Metrics
	log       *slog.Logger
	version   string
	syncEvery time.Duration
	stmtEvery time.Duration
	loadEvery time.Duration

	sem       chan struct{}
	slotsBusy atomic.Int64
	reload    chan struct{}

	mu      sync.Mutex
	workers map[int64]*running
	states  map[int64]*State
}

type running struct {
	cancel    context.CancelFunc
	done      chan struct{}
	updatedAt time.Time
}

// NewManager builds a manager. analyzer may be nil.
func NewManager(store *repository.Store, box *secret.Box, writer *Writer, analyzer Analyzer, events EventSink,
	settings Settings, metrics *obs.Metrics, log *slog.Logger, cfg Config) *Manager {
	if cfg.MaxConcurrent < 1 {
		cfg.MaxConcurrent = 4
	}
	if cfg.SyncInterval <= 0 {
		cfg.SyncInterval = 15 * time.Second
	}
	if cfg.StatementsInterval <= 0 {
		cfg.StatementsInterval = 60 * time.Second
	}
	return &Manager{store: store, box: box, writer: writer, analyzer: analyzer, events: events, settings: settings,
		metrics: metrics, log: log, version: cfg.Version, syncEvery: cfg.SyncInterval, stmtEvery: cfg.StatementsInterval, loadEvery: cfg.LoadInterval,
		sem: make(chan struct{}, cfg.MaxConcurrent), reload: make(chan struct{}, 1),
		workers: map[int64]*running{}, states: map[int64]*State{}}
}

// SetAnalyzer sets the analyzer before Run.
func (m *Manager) SetAnalyzer(a Analyzer) { m.analyzer = a }

// Reload asks the manager to re-read targets now (after a change via the API).
func (m *Manager) Reload() {
	select {
	case m.reload <- struct{}{}:
	default:
	}
}

// State returns the in-memory state of a target, if any.
func (m *Manager) State(id int64) (*State, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.states[id]
	return s, ok
}

// Run synchronises workers with the stored targets until ctx is cancelled,
// then stops every worker.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(m.syncEvery)
	defer t.Stop()
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

func (m *Manager) sync(ctx context.Context) {
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
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.workers {
		t, ok := wanted[id]
		if !ok || !t.UpdatedAt.Equal(r.updatedAt) {
			r.cancel()
			<-r.done
			delete(m.workers, id)
			if !ok {
				m.metrics.CollectFreshness.Delete(strconv.FormatInt(id, 10))
			}
		}
	}
	for id, t := range wanted {
		if _, ok := m.workers[id]; ok {
			continue
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

func (m *Manager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.workers {
		r.cancel()
		<-r.done
		delete(m.workers, id)
	}
	m.metrics.ActiveTargets.Set(0)
}
