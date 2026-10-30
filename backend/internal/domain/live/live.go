// Package live holds the live view of a monitored target: the latest
// collected data, the recent history of metrics, and the state of its
// collectors. The collector builds it; the rules, the incident recorder, and
// the API read it.
package live

import (
	"time"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/domain/report"
)

// View is an immutable copy of a target's state. Slices and maps must not
// be modified by readers.
type View struct {
	Capabilities *observe.Capabilities
	Activity     *observe.Activity
	Report       *report.Report
	Replication  *observe.Replication
	Tables       *observe.TableHealth
	// TableActivity are the busiest tables between the last two table
	// activity collections, of every database in scope.
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

// TimedValue is a metric value at a time; nil Value means unknown.
type TimedValue struct {
	At    time.Time `json:"at"`
	Value *float64  `json:"value"`
}

// Burst is a period of faster collection.
type Burst struct {
	Until  time.Time `json:"until"`
	Reason string    `json:"reason"`
}

// Latest returns the most recent value of a metric.
func (v View) Latest(metric string) (TimedValue, bool) {
	h := v.Points[metric]
	if len(h) == 0 {
		return TimedValue{}, false
	}
	return h[len(h)-1], true
}
