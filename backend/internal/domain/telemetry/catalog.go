package telemetry

// def maps one LockLens metric to an exported one.
type def struct {
	name, help string
	// label names the metric's own label (for example "database"); fixed
	// sets a constant label instead (sessions by state).
	label string
	fixed *Label
	// factor converts to the base unit (milliseconds to seconds).
	factor float64
}

func (d def) labels(value string) []Label {
	switch {
	case d.fixed != nil:
		return []Label{*d.fixed}
	case d.label != "":
		return []Label{{d.label, value}}
	}
	return nil
}

func (d def) scale() float64 {
	if d.factor == 0 {
		return 1
	}
	return d.factor
}

func state(s string) *Label { return &Label{"state", s} }

// gauges are the current values exported, by LockLens metric.
var gauges = map[string]def{
	"sessions.active":              {name: "locklens_pg_sessions", help: "Client sessions by state.", fixed: state("active")},
	"sessions.idle":                {name: "locklens_pg_sessions", help: "Client sessions by state.", fixed: state("idle")},
	"sessions.idle_in_transaction": {name: "locklens_pg_sessions", help: "Client sessions by state.", fixed: state("idle_in_transaction")},
	"sessions.waiting_lock":        {name: "locklens_pg_sessions", help: "Client sessions by state.", fixed: state("waiting_lock")},
	"sessions.unknown_state":       {name: "locklens_pg_sessions", help: "Client sessions by state.", fixed: state("unknown")},

	"connections.used":        {name: "locklens_pg_connections_used", help: "Connections in use (all backends that count against max_connections)."},
	"connections.max":         {name: "locklens_pg_connections_max", help: "max_connections."},
	"connections.reserved":    {name: "locklens_pg_connections_reserved", help: "Connection slots reserved for superusers and replication."},
	"connections.usage_ratio": {name: "locklens_pg_connections_usage_ratio", help: "Used connections / (max_connections - reserved slots)."},

	"blocking.blocked_sessions":      {name: "locklens_pg_blocked_sessions", help: "Sessions waiting for a lock held by another session."},
	"blocking.root_blockers":         {name: "locklens_pg_blocking_root_sessions", help: "Sessions at the root of a blocking chain."},
	"blocking.max_lock_wait_seconds": {name: "locklens_pg_lock_wait_max_seconds", help: "Longest current lock wait."},

	"transactions.open":               {name: "locklens_pg_transactions_open", help: "Open transactions."},
	"transactions.long":               {name: "locklens_pg_transactions_long", help: "Transactions open longer than the long-transaction threshold."},
	"transactions.oldest_age_seconds": {name: "locklens_pg_transaction_oldest_age_seconds", help: "Age of the oldest open transaction."},
	"transactions.idle_in_transaction_oldest_age_seconds": {name: "locklens_pg_idle_in_transaction_oldest_age_seconds",
		help: "Longest time a session has been idle in a transaction."},

	"cache.hit_ratio": {name: "locklens_pg_cache_hit_ratio", help: "Shared buffer hits / (hits + reads) over the latest collection interval."},

	"replication.replicas":         {name: "locklens_pg_replicas", help: "Connected replicas (primary)."},
	"replication.replay_lag_bytes": {name: "locklens_pg_replica_replay_lag_bytes", help: "WAL a replica has not replayed yet (primary).", label: "replica"},
	"slots.retained_bytes":         {name: "locklens_pg_replication_slot_retained_bytes", help: "WAL a replication slot keeps.", label: "slot"},
	"standby.replay_delay_bytes":   {name: "locklens_pg_standby_replay_delay_bytes", help: "WAL received but not replayed yet (standby)."},

	"db.size_bytes": {name: "locklens_pg_database_size_bytes", help: "Database size.", label: "database"},
	"db.xid_age":    {name: "locklens_pg_database_xid_age", help: "age(datfrozenxid): transactions since the database was last frozen.", label: "database"},

	"host.cpu_utilization_ratio":  {name: "locklens_host_cpu_utilization_ratio", help: "Host CPU utilisation (node_exporter)."},
	"host.memory_available_bytes": {name: "locklens_host_memory_available_bytes", help: "Host memory available (node_exporter)."},
	"host.memory_total_bytes":     {name: "locklens_host_memory_total_bytes", help: "Host memory (node_exporter)."},
	"host.filesystem_used_ratio":  {name: "locklens_host_filesystem_used_ratio", help: "Used share of a filesystem (node_exporter).", label: "mountpoint"},
}

// counters are the running totals exported, by LockLens metric (a rate or
// a delta).
var counters = map[string]def{
	"xact.commit.per_s":                 {name: "locklens_pg_xact_commit_total", help: "Committed transactions."},
	"xact.rollback.per_s":               {name: "locklens_pg_xact_rollback_total", help: "Rolled back transactions."},
	"blocks.read.per_s":                 {name: "locklens_pg_blocks_read_total", help: "Blocks read from disk or the OS cache."},
	"blocks.hit.per_s":                  {name: "locklens_pg_blocks_hit_total", help: "Blocks found in shared buffers."},
	"tuples.returned.per_s":             {name: "locklens_pg_tuples_returned_total", help: "Rows returned by sequential scans."},
	"tuples.fetched.per_s":              {name: "locklens_pg_tuples_fetched_total", help: "Rows fetched by index scans."},
	"tuples.inserted.per_s":             {name: "locklens_pg_tuples_inserted_total", help: "Rows inserted."},
	"tuples.updated.per_s":              {name: "locklens_pg_tuples_updated_total", help: "Rows updated."},
	"tuples.deleted.per_s":              {name: "locklens_pg_tuples_deleted_total", help: "Rows deleted."},
	"temp.files.per_s":                  {name: "locklens_pg_temp_files_total", help: "Temporary files created."},
	"temp.bytes.per_s":                  {name: "locklens_pg_temp_bytes_total", help: "Bytes written to temporary files."},
	"deadlocks.delta":                   {name: "locklens_pg_deadlocks_total", help: "Deadlocks detected."},
	"checkpoints.timed.delta":           {name: "locklens_pg_checkpoints_timed_total", help: "Scheduled checkpoints."},
	"checkpoints.requested.delta":       {name: "locklens_pg_checkpoints_requested_total", help: "Requested checkpoints."},
	"checkpoint.buffers_written.per_s":  {name: "locklens_pg_checkpoint_buffers_written_total", help: "Buffers written by checkpoints."},
	"bgwriter.buffers_clean.per_s":      {name: "locklens_pg_bgwriter_buffers_clean_total", help: "Buffers written by the background writer."},
	"buffers.alloc.per_s":               {name: "locklens_pg_buffers_alloc_total", help: "Buffers allocated."},
	"io.reads.per_s":                    {name: "locklens_pg_io_reads_total", help: "Read operations (pg_stat_io, PostgreSQL 16+)."},
	"io.writes.per_s":                   {name: "locklens_pg_io_writes_total", help: "Write operations (pg_stat_io, PostgreSQL 16+)."},
	"wal.bytes.per_s":                   {name: "locklens_pg_wal_bytes_total", help: "WAL generated (primary)."},
	"wal.received_bytes.per_s":          {name: "locklens_pg_wal_received_bytes_total", help: "WAL received (standby)."},
	"archiver.archived.delta":           {name: "locklens_pg_archiver_archived_total", help: "WAL files archived."},
	"archiver.failed.delta":             {name: "locklens_pg_archiver_failed_total", help: "Failed WAL archiving attempts."},
	"statements.calls.per_s":            {name: "locklens_pg_statement_calls_total", help: "Statement calls (pg_stat_statements)."},
	"statements.exec_ms.per_s":          {name: "locklens_pg_statement_exec_seconds_total", help: "Statement execution time (pg_stat_statements).", factor: 0.001},
	"host.disk_read_bytes.per_s":        {name: "locklens_host_disk_read_bytes_total", help: "Bytes read from disks (node_exporter)."},
	"host.disk_write_bytes.per_s":       {name: "locklens_host_disk_written_bytes_total", help: "Bytes written to disks (node_exporter)."},
	"host.network_receive_bytes.per_s":  {name: "locklens_host_network_receive_bytes_total", help: "Bytes received (node_exporter)."},
	"host.network_transmit_bytes.per_s": {name: "locklens_host_network_transmit_bytes_total", help: "Bytes sent (node_exporter)."},
}
