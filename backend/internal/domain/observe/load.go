package observe

import "time"

// Wait classes of sampled sessions. They are pg_stat_activity.wait_event_type
// values, plus three LockLens classes: CPU for an active session that reports
// no wait event (running on a CPU, or waiting on something PostgreSQL does not
// instrument), IdleTx for a session idle inside an open transaction, and
// Unknown when the observer cannot see the session's state.
const (
	WaitCPU     = "CPU"
	WaitIdleTx  = "IdleTx"
	WaitUnknown = "Unknown"
)

// WaitClass classifies one pg_stat_activity row. ok is false for rows that
// are not load (idle sessions).
func WaitClass(state, waitEventType *string) (class string, ok bool) {
	switch {
	case state == nil:
		return WaitUnknown, true
	case *state == "idle":
		return "", false
	case *state == "idle in transaction" || *state == "idle in transaction (aborted)":
		return WaitIdleTx, true
	case waitEventType == nil || *waitEventType == "":
		return WaitCPU, true
	}
	return *waitEventType, true
}

// LoadGroup counts the sampled sessions that shared the same wait, statement,
// and client during one bucket. Samples is summed over the bucket's ticks,
// so Samples / Ticks is the group's average number of sessions.
type LoadGroup struct {
	WaitClass   string `json:"wait_class"`
	WaitEvent   string `json:"wait_event"`
	BackendType string `json:"backend_type"`
	DBID        uint32 `json:"dbid"`
	DatName     string `json:"datname"`
	UserID      uint32 `json:"userid"`
	UserName    string `json:"username"`
	Application string `json:"application_name"`
	// QueryID is pg_stat_activity.query_id: the top-level statement's
	// pg_stat_statements queryid, or 0 when it is not computed.
	QueryID int64 `json:"queryid,string"`
	Samples int   `json:"samples"`
}

// LoadBucket holds the samples taken in one fixed period (LoadBucketSize).
type LoadBucket struct {
	At              time.Time   `json:"at"`
	Ticks           int         `json:"ticks"`
	IntervalSeconds float64     `json:"interval_seconds"`
	Groups          []LoadGroup `json:"groups"`
}

// LoadBucketSize is the period samples are aggregated over before storage.
const LoadBucketSize = 15 * time.Second
