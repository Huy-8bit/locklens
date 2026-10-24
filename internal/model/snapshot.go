// Package model defines the snapshot data that LockLens collects from
// PostgreSQL and persists to disk.
//
// A Snapshot holds raw observations only. Derived values (ages, graph counts,
// root blockers) are computed by package report so that an old snapshot file
// can be re-analysed by a newer LockLens without the derived data going stale.
//
// Every timestamp is stored in UTC. Pointer fields are nullable because
// PostgreSQL returns NULL for many columns, either by definition (xact_start is
// NULL outside a transaction) or because the observing role lacks permission to
// see another role's session details.
package model

import "time"

// SchemaVersion is the snapshot schema version written by this build.
// Bump it whenever a change would make an older reader misinterpret the file;
// see docs/snapshot-format.md for the compatibility rules.
const SchemaVersion = 1

// Snapshot is one observation of lock contention on a PostgreSQL server.
//
// A snapshot is assembled from several queries run inside one read-only
// transaction. It is not an atomic picture of the server: pg_locks and
// pg_blocking_pids are read live, while pg_stat_activity is cached for the
// duration of the transaction.
type Snapshot struct {
	SchemaVersion int `json:"schema_version"`
	// CollectedAt is the client clock (UTC) when collection finished.
	CollectedAt time.Time `json:"collected_at"`
	// ServerTime is clock_timestamp() on the server at collection time.
	// All ages are computed relative to it to avoid client/server clock skew.
	ServerTime time.Time  `json:"server_time"`
	Tool       Tool       `json:"tool"`
	Collection Collection `json:"collection"`
	Server     Server     `json:"server"`
	// Sessions contains every backend involved in blocking plus every client
	// backend with an open transaction. LockLens' own connections are excluded.
	Sessions []Session `json:"sessions"`
	// Edges are blocker → waiter relations reported by pg_blocking_pids,
	// deduplicated. Blocker PID 0 denotes a prepared transaction.
	Edges                []Edge                `json:"edges"`
	PreparedTransactions []PreparedTransaction `json:"prepared_transactions"`
	Warnings             []Warning             `json:"warnings"`
}

// Tool identifies the LockLens build that produced the snapshot.
type Tool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Collection records the options and cost of the collection run.
type Collection struct {
	DurationMS     int64 `json:"duration_ms"`
	IncludeQuery   bool  `json:"include_query"`
	QueryMaxLength int   `json:"query_max_length"`
	// MinTxAgeSeconds is the long-transaction threshold that was in effect.
	// It is the default threshold when the snapshot is shown later.
	MinTxAgeSeconds float64 `json:"min_tx_age_seconds"`
	// Atomic is always false; it is recorded so consumers do not assume
	// otherwise.
	Atomic bool `json:"atomic"`
}

// Server describes the observed server. It never contains credentials.
type Server struct {
	Host       string `json:"host"`
	Port       uint16 `json:"port"`
	Database   string `json:"database"`
	User       string `json:"user"`
	Version    string `json:"version"`
	VersionNum int    `json:"version_num"`
	// ReadAllStats reports whether the observing role has the privileges of
	// pg_read_all_stats (or is a superuser), i.e. can see every session's
	// details in pg_stat_activity.
	ReadAllStats bool `json:"read_all_stats"`
	// TrackActivities mirrors the track_activities setting. When it is off,
	// state, wait events, and query text are not maintained by the server.
	TrackActivities bool `json:"track_activities"`
}

// Session is one backend from pg_stat_activity.
type Session struct {
	PID int `json:"pid"`
	// LeaderPID is set for parallel workers and NULL otherwise.
	LeaderPID       *int       `json:"leader_pid"`
	Database        *string    `json:"database"`
	User            *string    `json:"user"`
	ApplicationName *string    `json:"application_name"`
	ClientAddr      *string    `json:"client_addr"`
	BackendType     *string    `json:"backend_type"`
	State           *string    `json:"state"`
	WaitEventType   *string    `json:"wait_event_type"`
	WaitEvent       *string    `json:"wait_event"`
	BackendStart    *time.Time `json:"backend_start"`
	XactStart       *time.Time `json:"xact_start"`
	// QueryStart is when the current query started, or when the last query
	// started if the session is not active. It is NOT when a lock wait began.
	QueryStart *time.Time `json:"query_start"`
	// StateChange is when State last changed. It is NOT when a lock wait began.
	StateChange *time.Time `json:"state_change"`
	BackendXID  *string    `json:"backend_xid"`
	// Query is present only when collection ran with --include-query and the
	// observing role may see the text. It is truncated to QueryMaxLength runes.
	Query          *string `json:"query,omitempty"`
	QueryTruncated bool    `json:"query_truncated,omitempty"`
	// DetailsVisible is false when PostgreSQL hides this session's details
	// from the observing role; most fields are then NULL.
	DetailsVisible bool `json:"details_visible"`
	// ParallelWorkers counts parallel workers whose leader is this session.
	ParallelWorkers int `json:"parallel_workers"`
	// WaitingLocks lists ungranted pg_locks rows of this session (and of its
	// parallel workers).
	WaitingLocks []WaitingLock `json:"waiting_locks"`
}

// WaitingLock is a lock request that has not been granted yet.
// It describes what the waiter asked for; it does not identify which lock
// held by a blocker conflicts with it.
type WaitingLock struct {
	PID         int     `json:"pid"`
	LockType    string  `json:"locktype"`
	Mode        string  `json:"mode"`
	Database    *string `json:"database"`
	RelationOID *uint32 `json:"relation_oid"`
	// Relation is the qualified relation name; it is resolved only for
	// relations in the database LockLens is connected to.
	Relation      *string `json:"relation"`
	Page          *int32  `json:"page"`
	Tuple         *int16  `json:"tuple"`
	VirtualXID    *string `json:"virtualxid"`
	TransactionID *string `json:"transactionid"`
	ClassID       *uint32 `json:"classid"`
	ObjID         *uint32 `json:"objid"`
	ObjSubID      *int16  `json:"objsubid"`
	// WaitStart is pg_locks.waitstart. It can be NULL for a very short time
	// after the wait began.
	WaitStart *time.Time `json:"wait_start"`
}

// Edge is a blocker → waiter relation.
type Edge struct {
	Blocker int `json:"blocker"`
	Waiter  int `json:"waiter"`
}

// PreparedTransaction is a row from pg_prepared_xacts. PostgreSQL reports a
// blocking prepared transaction as PID 0 and does not say which one it is.
type PreparedTransaction struct {
	GID         string    `json:"gid"`
	Transaction string    `json:"transaction"`
	Prepared    time.Time `json:"prepared"`
	Owner       string    `json:"owner"`
	Database    string    `json:"database"`
}

// Warning describes a data completeness or interpretation caveat.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Warning codes. They are part of the JSON contract.
const (
	WarnRestrictedVisibility = "restricted_visibility"
	WarnTrackActivitiesOff   = "track_activities_off"
	WarnMissingSession       = "missing_session"
	WarnPreparedBlocker      = "prepared_transaction_blocker"
	WarnCycle                = "cycle_detected"
	WarnSelfBlocking         = "locklens_session_in_graph"
	WarnUnsupportedVersion   = "unsupported_server_version"
)

// SessionByPID returns the session with the given PID, if present.
func (s *Snapshot) SessionByPID(pid int) (*Session, bool) {
	for i := range s.Sessions {
		if s.Sessions[i].PID == pid {
			return &s.Sessions[i], true
		}
	}
	return nil, false
}
