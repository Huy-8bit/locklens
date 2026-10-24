package collector

import (
	"sync"
	"time"

	"github.com/locklens/locklens/internal/observe"
	"github.com/locklens/locklens/internal/report"
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
}

// maxStatementHistory bounds the in-memory statements history used by the
// workload rules (about two hours at the default 60 s interval).
const maxStatementHistory = 120

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
	mu           sync.RWMutex
	caps         *observe.Capabilities
	activity     *observe.Activity
	report       *report.Report
	replication  *observe.Replication
	tables       *observe.TableHealth
	host         *observe.HostMetrics
	hostError    *string
	statements   []observe.StatementsCollection
	points       map[string][]TimedValue
	collectors   map[string]CollectorStatus
	connected    bool
	connectError *string
	connectAt    *time.Time
}

func newState() *State {
	return &State{points: map[string][]TimedValue{}, collectors: map[string]CollectorStatus{}}
}

// View is an immutable copy of a target's state. Slices and maps must not
// be modified by readers.
type View struct {
	Capabilities *observe.Capabilities
	Activity     *observe.Activity
	Report       *report.Report
	Replication  *observe.Replication
	Tables       *observe.TableHealth
	Host         *observe.HostMetrics
	HostError    *string
	Statements   []observe.StatementsCollection
	Points       map[string][]TimedValue
	Collectors   map[string]CollectorStatus
	Connected    bool
	ConnectError *string
	ConnectAt    *time.Time
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
		Tables: s.tables, Host: s.host, HostError: s.hostError, Statements: s.statements,
		Points: pts, Collectors: colls, Connected: s.connected, ConnectError: s.connectError, ConnectAt: s.connectAt,
	}
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
