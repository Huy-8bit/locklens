package collector

import (
	"sync"
	"time"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/domain/report"
)

// CollectorStatus is the outcome of a collector's most recent runs.
type CollectorStatus struct {
	Name                string     `json:"name"`
	IntervalSeconds     float64    `json:"interval_seconds"`
	Enabled             bool       `json:"enabled"`
	DisabledReason      string     `json:"disabled_reason,omitempty"`
	LastAttempt         *time.Time `json:"last_attempt"`
	LastSuccess         *time.Time `json:"last_success"`
	LastError           *string    `json:"last_error"`
	LastDurationMS      *int64     `json:"last_duration_ms"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	// BaseIntervalSeconds is the configured interval; IntervalSeconds is the
	// one in effect (shorter during a burst, longer when slowed down).
	BaseIntervalSeconds float64 `json:"base_interval_seconds"`
	// Burst is true while the target is collected faster (adaptive sampling).
	Burst bool `json:"burst,omitempty"`
	// ThrottleReason says why the collector runs less often than configured.
	ThrottleReason string `json:"throttle_reason,omitempty"`
}

// maxStatementHistory bounds the in-memory statements history used by the
// workload rules (two hours at the default 15 s interval).
const maxStatementHistory = 480

// maxPointHistory bounds per-metric in-memory history used by rules.
const maxPointHistory = 360

// TimedValue is a metric value at a time; nil Value means unknown.
type TimedValue struct {
	At    time.Time `json:"at"`
	Value *float64  `json:"value"`
}

// State is the in-memory view of one target, updated by its worker and read
// by the API and the analyzer. Access through Snapshot.
type State struct {
	mu          sync.RWMutex
	caps        *observe.Capabilities
	activity    *observe.Activity
	report      *report.Report
	replication *observe.Replication
	tables      *observe.TableHealth
	// tableActivity are the busiest tables of the last minute.
	tableActivity []observe.TableActivity
	host          *observe.HostMetrics
	hostError     *string
	statements    []observe.StatementsCollection
	load          []observe.LoadBucket
	points        map[string][]TimedValue
	collectors    map[string]CollectorStatus
	connected     bool
	connectError  *string
	connectAt     *time.Time

	burstUntil  time.Time
	burstReason string
	// runSoon are collectors asked to run now (Manager.RunSoon).
	runSoon map[string]bool
	wake    chan struct{}
}

// requestRun asks the worker to run a collector as soon as possible.
func (s *State) requestRun(name string) {
	s.mu.Lock()
	if s.runSoon == nil {
		s.runSoon = map[string]bool{}
	}
	s.runSoon[name] = true
	wake := s.wake
	s.mu.Unlock()
	select {
	case wake <- struct{}{}:
	default:
	}
}

// takeRunSoon returns and clears the collectors asked to run now.
func (s *State) takeRunSoon() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.runSoon
	s.runSoon = nil
	return r
}

// Burst is a period of faster collection.
type Burst struct {
	Until  time.Time `json:"until"`
	Reason string    `json:"reason"`
}

// setBurst starts or extends faster collection until a time and wakes the
// worker so the shorter intervals apply at once.
func (s *State) setBurst(until time.Time, reason string) {
	s.mu.Lock()
	started := !s.burstUntil.After(time.Now())
	if until.After(s.burstUntil) {
		s.burstUntil, s.burstReason = until, reason
	}
	wake := s.wake
	s.mu.Unlock()
	if started && wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

// burst returns the current burst, if any.
func (s *State) burst(now time.Time) (Burst, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !now.Before(s.burstUntil) {
		return Burst{}, false
	}
	return Burst{Until: s.burstUntil, Reason: s.burstReason}, true
}

func newState() *State {
	return &State{points: map[string][]TimedValue{}, collectors: map[string]CollectorStatus{}, wake: make(chan struct{}, 1)}
}

// View is an immutable copy of a target's state. Slices and maps must not
// be modified by readers.
type View struct {
	Capabilities *observe.Capabilities
	Activity     *observe.Activity
	Report       *report.Report
	Replication  *observe.Replication
	Tables       *observe.TableHealth
	// TableActivity are the busiest tables between the last two table
	// activity collections.
	TableActivity []observe.TableActivity
	Host          *observe.HostMetrics
	HostError     *string
	Statements    []observe.StatementsCollection
	// Load holds the last finished active session sampling buckets.
	Load         []observe.LoadBucket
	Points       map[string][]TimedValue
	Collectors   map[string]CollectorStatus
	Connected    bool
	ConnectError *string
	ConnectAt    *time.Time
	// Burst is set while the target is collected faster.
	Burst *Burst
}

// Snapshot returns a consistent copy.
func (s *State) Snapshot() View {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pts := make(map[string][]TimedValue, len(s.points))
	for k, v := range s.points {
		pts[k] = v
	}
	colls := make(map[string]CollectorStatus, len(s.collectors))
	for k, v := range s.collectors {
		colls[k] = v
	}
	return View{
		Capabilities: s.caps, Activity: s.activity, Report: s.report, Replication: s.replication,
		Tables: s.tables, TableActivity: s.tableActivity, Host: s.host, HostError: s.hostError, Statements: s.statements, Load: s.load,
		Points: pts, Collectors: colls, Connected: s.connected, ConnectError: s.connectError, ConnectAt: s.connectAt,
		Burst: s.burstView(),
	}
}

// burstView is the current burst; the caller holds s.mu.
func (s *State) burstView() *Burst {
	if !time.Now().Before(s.burstUntil) {
		return nil
	}
	return &Burst{Until: s.burstUntil, Reason: s.burstReason}
}

// Latest returns the most recent value of a metric.
func (v View) Latest(metric string) (TimedValue, bool) {
	h := v.Points[metric]
	if len(h) == 0 {
		return TimedValue{}, false
	}
	return h[len(h)-1], true
}

func (s *State) addPoints(at time.Time, points []observe.Point) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range points {
		key := p.Metric
		if p.Label != "" {
			key += "{" + p.Label + "}"
		}
		h := append(s.points[key], TimedValue{At: at, Value: p.Value})
		if len(h) > maxPointHistory {
			h = h[len(h)-maxPointHistory:]
		}
		// Copy-on-write so views taken earlier stay unchanged.
		s.points[key] = append([]TimedValue(nil), h...)
	}
}

func (s *State) setCollector(st CollectorStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.collectors[st.Name] = st
}

// Target monitoring states.
const (
	StatePending     = "pending"
	StateHealthy     = "healthy"
	StateDegraded    = "degraded"
	StateUnreachable = "unreachable"
	StateDisabled    = "disabled"
)

// ComputeState derives the documented target state:
//   - disabled: collection switched off;
//   - pending: no connection attempt yet;
//   - unreachable: the last connection attempt failed, or the activity
//     collector has not succeeded within three intervals;
//   - degraded: connected, but a collector's latest run failed, or the data is
//     partial (role without pg_read_all_stats, track_activities off);
//   - healthy: everything else.
//
// It describes LockLens' ability to monitor the target, not the health of
// the database itself.
func ComputeState(enabled bool, v View, interval time.Duration, now time.Time) string {
	if !enabled {
		return StateDisabled
	}
	if v.ConnectAt == nil {
		return StatePending
	}
	if !v.Connected {
		return StateUnreachable
	}
	a, ok := v.Collectors[CollectorActivity]
	if !ok || a.LastAttempt == nil {
		return StatePending
	}
	if a.LastSuccess == nil || now.Sub(*a.LastSuccess) > 3*interval+15*time.Second {
		return StateUnreachable
	}
	for _, c := range v.Collectors {
		if c.Enabled && c.LastError != nil {
			return StateDegraded
		}
	}
	if v.Capabilities != nil && (!v.Capabilities.ReadAllStats || !v.Capabilities.TrackActivities) {
		return StateDegraded
	}
	return StateHealthy
}

func (s *State) addLoad(b observe.LoadBucket) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := append(s.load, b)
	if len(h) > maxLoadHistory {
		h = h[len(h)-maxLoadHistory:]
	}
	// Copy-on-write so views taken earlier stay unchanged.
	s.load = append([]observe.LoadBucket(nil), h...)
}
