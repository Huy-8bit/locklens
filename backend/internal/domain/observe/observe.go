// Package observe defines the observations collectors produce and that the
// rule engine, incident recorder, storage, and API consume.
//
// Pointer fields are nullable: nil means "unknown or not available", which is
// never the same as zero.
package observe

import (
	"time"

	"github.com/locklens/locklens/internal/domain/model"
)

// Capabilities describes what a target's server and the monitoring role can
// provide. Missing capabilities degrade individual views, never the target.
type Capabilities struct {
	ServerVersion    string    `json:"server_version"`
	VersionNum       int       `json:"server_version_num"`
	IsStandby        bool      `json:"is_standby"`
	ReadAllStats     bool      `json:"read_all_stats"`
	TrackActivities  bool      `json:"track_activities"`
	TrackIOTiming    bool      `json:"track_io_timing"`
	StatIO           bool      `json:"pg_stat_io"`
	Checkpointer     bool      `json:"pg_stat_checkpointer"`
	StatStatements   bool      `json:"pg_stat_statements"`
	StatementsSchema string    `json:"pg_stat_statements_schema,omitempty"`
	StatementsVer    string    `json:"pg_stat_statements_version,omitempty"`
	StatementsReason string    `json:"pg_stat_statements_reason,omitempty"`
	DetectedAt       time.Time `json:"detected_at"`
}

// Major returns the server major version (e.g. 17).
func (c Capabilities) Major() int { return c.VersionNum / 10000 }

// ActivityCounts summarises pg_stat_activity.
type ActivityCounts struct {
	// Client backends excluding LockLens' own connections.
	ClientTotal     int `json:"client_total"`
	Active          int `json:"active"`
	Idle            int `json:"idle"`
	IdleInTx        int `json:"idle_in_transaction"`
	IdleInTxAborted int `json:"idle_in_transaction_aborted"`
	// UnknownState counts sessions whose state is hidden (insufficient
	// privileges) or not reported (track_activities off).
	UnknownState int `json:"unknown_state"`
	WaitingLock  int `json:"waiting_lock"`
	// UsedConnections counts every client backend, including LockLens', since
	// all of them consume max_connections slots.
	UsedConnections     int `json:"used_connections"`
	MaxConnections      int `json:"max_connections"`
	ReservedConnections int `json:"reserved_connections"`
}

// Point is one time-series value. A nil Value means "collected but unknown".
type Point struct {
	IntervalSeconds *float64 `json:"interval_seconds,omitempty"`
	Metric          string   `json:"metric"`
	Label           string   `json:"label,omitempty"`
	Value           *float64 `json:"value"`
}

// F returns a pointer to v.
func F(v float64) *float64 { return &v }

// QueryKey identifies a pg_stat_statements entry. queryid alone is not
// enough: the same value can appear for different databases, users, and
// top-level/nested execution, and it is not stable across major versions.
type QueryKey struct {
	ServerMajor int    `json:"server_major"`
	DBID        uint32 `json:"dbid"`
	UserID      uint32 `json:"userid"`
	TopLevel    bool   `json:"toplevel"`
	// QueryID is encoded as a JSON string: JavaScript numbers cannot hold
	// every 64-bit value.
	QueryID int64 `json:"queryid,string"`
}

// QueryDelta is the change of one statement's counters between collections.
type QueryDelta struct {
	Key      QueryKey `json:"key"`
	DatName  string   `json:"datname"`
	UserName string   `json:"username"`
	// Text is the normalised statement, only when the target opted in.
	Text *string `json:"text,omitempty"`
	// Command is the statement class (see ClassifyCommand), known even when
	// the text is not collected.
	Command string   `json:"command"`
	Calls   int64    `json:"calls"`
	TotalMS float64  `json:"total_exec_ms"`
	Rows    int64    `json:"rows"`
	BlkHit  int64    `json:"shared_blks_hit"`
	BlkRead int64    `json:"shared_blks_read"`
	TempW   int64    `json:"temp_blks_written"`
	WAL     *float64 `json:"wal_bytes"`
}

// StatementsCollection is the result of one pg_stat_statements read.
type StatementsCollection struct {
	At time.Time `json:"at"`
	// Interval is the time since the previous collection; nil for the first
	// collection after start (a baseline without deltas).
	Interval *float64     `json:"interval_seconds"`
	Entries  int          `json:"entries"`
	Hidden   int          `json:"hidden"`
	Dealloc  *int64       `json:"dealloc_delta"`
	Reset    bool         `json:"reset_detected"`
	Deltas   []QueryDelta `json:"deltas"`
	// Truncated counts changed entries not stored because of the per-collection cap.
	Truncated int `json:"truncated"`
}

// TableStat is one row of table statistics. Tuple counts are estimates.
type TableStat struct {
	RelID            uint32     `json:"relid"`
	DatName          string     `json:"datname"`
	Schema           string     `json:"schema"`
	Name             string     `json:"name"`
	LiveTuples       *int64     `json:"n_live_tup"`
	DeadTuples       *int64     `json:"n_dead_tup"`
	ModSinceAnalyze  *int64     `json:"n_mod_since_analyze"`
	LastVacuum       *time.Time `json:"last_vacuum"`
	LastAutovacuum   *time.Time `json:"last_autovacuum"`
	LastAnalyze      *time.Time `json:"last_analyze"`
	LastAutoanalyze  *time.Time `json:"last_autoanalyze"`
	VacuumCount      *int64     `json:"vacuum_count"`
	AutovacuumCount  *int64     `json:"autovacuum_count"`
	AnalyzeCount     *int64     `json:"analyze_count"`
	AutoanalyzeCount *int64     `json:"autoanalyze_count"`
	TableBytes       *int64     `json:"table_bytes"`
	IndexesBytes     *int64     `json:"indexes_bytes"`
	TotalBytes       *int64     `json:"total_bytes"`
	FrozenXIDAge     *int64     `json:"frozenxid_age"`
}

// IndexStat is one row of index statistics.
type IndexStat struct {
	DatName      string `json:"datname"`
	IndexRelID   uint32 `json:"indexrelid"`
	RelID        uint32 `json:"relid"`
	Schema       string `json:"schema"`
	Table        string `json:"table"`
	Name         string `json:"name"`
	Scans        *int64 `json:"idx_scan"`
	Bytes        *int64 `json:"index_bytes"`
	IsUnique     bool   `json:"is_unique"`
	IsPrimary    bool   `json:"is_primary"`
	IsConstraint bool   `json:"is_constraint"`
}

// DatabaseAge is the transaction ID age of one database.
type DatabaseAge struct {
	DatName   string `json:"datname"`
	XIDAge    *int64 `json:"xid_age"`
	MXIDAge   *int64 `json:"mxid_age"`
	SizeBytes *int64 `json:"size_bytes"`
}

// TableHealth is the heavy, slow-interval collection.
type TableHealth struct {
	At         time.Time     `json:"at"`
	DatName    string        `json:"datname"`
	SizeSource string        `json:"size_source"`
	Tables     []TableStat   `json:"tables"`
	Indexes    []IndexStat   `json:"indexes"`
	Databases  []DatabaseAge `json:"databases"`
	// Settings relevant to vacuum, as reported by the server.
	Settings map[string]string `json:"settings"`
	Warnings []string          `json:"warnings"`
}

// TableActivity is what one table did between two collections of
// pg_stat_user_tables (rates per second).
type TableActivity struct {
	DatName         string  `json:"datname"`
	RelID           uint32  `json:"relid"`
	Schema          string  `json:"schema"`
	Name            string  `json:"name"`
	SeqScansPerSec  float64 `json:"seq_scans_per_s"`
	SeqRowsPerSec   float64 `json:"seq_rows_per_s"`
	IdxScansPerSec  float64 `json:"idx_scans_per_s"`
	IdxRowsPerSec   float64 `json:"idx_rows_per_s"`
	WritesPerSec    float64 `json:"writes_per_s"`
	DeadRows        int64   `json:"dead_rows"`
	LiveRows        int64   `json:"live_rows"`
	IntervalSeconds float64 `json:"interval_seconds"`
}

// VacuumProgress is one row of pg_stat_progress_vacuum.
type VacuumProgress struct {
	PID     int     `json:"pid"`
	DatName *string `json:"datname"`
	// Relation and Phase are NULL for other roles' vacuums without
	// pg_read_all_stats.
	Relation         *string `json:"relation"`
	Phase            *string `json:"phase"`
	HeapBlksTotal    *int64  `json:"heap_blks_total"`
	HeapBlksScanned  *int64  `json:"heap_blks_scanned"`
	HeapBlksVacuumed *int64  `json:"heap_blks_vacuumed"`
	IndexVacuumCount *int64  `json:"index_vacuum_count"`
}

// Replica is one row of pg_stat_replication on a primary.
type Replica struct {
	PID             int        `json:"pid"`
	UserName        *string    `json:"usename"`
	ApplicationName string     `json:"application_name"`
	ClientAddr      *string    `json:"client_addr"`
	State           *string    `json:"state"`
	SyncState       *string    `json:"sync_state"`
	SentLSN         *string    `json:"sent_lsn"`
	WriteLSN        *string    `json:"write_lsn"`
	FlushLSN        *string    `json:"flush_lsn"`
	ReplayLSN       *string    `json:"replay_lsn"`
	SentLagBytes    *float64   `json:"sent_lag_bytes"`
	WriteLagBytes   *float64   `json:"write_lag_bytes"`
	FlushLagBytes   *float64   `json:"flush_lag_bytes"`
	ReplayLagBytes  *float64   `json:"replay_lag_bytes"`
	WriteLagSec     *float64   `json:"write_lag_seconds"`
	FlushLagSec     *float64   `json:"flush_lag_seconds"`
	ReplayLagSec    *float64   `json:"replay_lag_seconds"`
	ReplyTime       *time.Time `json:"reply_time"`
}

// Slot is one replication slot.
type Slot struct {
	Name          string     `json:"slot_name"`
	Type          string     `json:"slot_type"`
	Active        bool       `json:"active"`
	WALStatus     *string    `json:"wal_status"`
	RestartLSN    *string    `json:"restart_lsn"`
	RetainedBytes *float64   `json:"retained_bytes"`
	SafeWALSize   *int64     `json:"safe_wal_size"`
	InactiveSince *time.Time `json:"inactive_since"`
}

// Standby describes a server in recovery.
type Standby struct {
	ReceiveLSN         *string    `json:"receive_lsn"`
	ReplayLSN          *string    `json:"replay_lsn"`
	ReplayDelayBytes   *float64   `json:"replay_delay_bytes"`
	LastReplayTime     *time.Time `json:"last_xact_replay_timestamp"`
	ReplayPauseState   *string    `json:"replay_pause_state"`
	ReceiverStatus     *string    `json:"wal_receiver_status"`
	SenderHost         *string    `json:"sender_host"`
	ReceiverSlot       *string    `json:"slot_name"`
	LastMessageReceipt *time.Time `json:"last_msg_receipt_time"`
}

// Archiver is pg_stat_archiver.
type Archiver struct {
	ArchiveMode      string     `json:"archive_mode"`
	ArchivedCount    int64      `json:"archived_count"`
	FailedCount      int64      `json:"failed_count"`
	LastArchivedTime *time.Time `json:"last_archived_time"`
	LastFailedTime   *time.Time `json:"last_failed_time"`
	LastFailedWAL    *string    `json:"last_failed_wal"`
}

// Replication is one replication/WAL collection.
type Replication struct {
	At         time.Time `json:"at"`
	Role       string    `json:"role"`
	CurrentLSN *string   `json:"current_lsn"`
	Replicas   []Replica `json:"replicas"`
	Slots      []Slot    `json:"slots"`
	Standby    *Standby  `json:"standby"`
	Archiver   *Archiver `json:"archiver"`
	Warnings   []string  `json:"warnings"`
}

// Activity is one light collection: the blocking snapshot plus counts.
type Activity struct {
	At       time.Time        `json:"at"`
	Snapshot *model.Snapshot  `json:"snapshot"`
	Counts   ActivityCounts   `json:"counts"`
	Vacuum   []VacuumProgress `json:"vacuum_progress"`
	// Groups counts client connections by database, user, application, and
	// state (LockLens' own sessions excluded).
	Groups []ConnectionGroup `json:"connection_groups,omitempty"`
}

// ConnectionGroup is a number of client connections sharing database, user,
// application name, and state. Values the monitoring role cannot see are
// empty (state "unknown").
type ConnectionGroup struct {
	Database    string `json:"database"`
	User        string `json:"user"`
	Application string `json:"application"`
	ClientAddr  string `json:"client_addr"`
	State       string `json:"state"`
	Count       int    `json:"count"`
	// OldestSeconds is the age of the oldest connection in the group.
	OldestSeconds *float64 `json:"oldest_seconds"`
}

// HostMetrics are values derived from a node_exporter scrape.
type HostMetrics struct {
	At             time.Time    `json:"at"`
	CPUUtilization *float64     `json:"cpu_utilization_ratio"`
	MemTotalBytes  *float64     `json:"memory_total_bytes"`
	MemAvailBytes  *float64     `json:"memory_available_bytes"`
	Filesystems    []Filesystem `json:"filesystems"`
	DiskReadBps    *float64     `json:"disk_read_bytes_per_second"`
	DiskWriteBps   *float64     `json:"disk_write_bytes_per_second"`
	NetRxBps       *float64     `json:"network_receive_bytes_per_second"`
	NetTxBps       *float64     `json:"network_transmit_bytes_per_second"`
}

// Filesystem is one mounted filesystem reported by node_exporter.
type Filesystem struct {
	Mountpoint string  `json:"mountpoint"`
	FSType     string  `json:"fstype"`
	SizeBytes  float64 `json:"size_bytes"`
	AvailBytes float64 `json:"avail_bytes"`
}
