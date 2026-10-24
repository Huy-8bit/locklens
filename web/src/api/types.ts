// Types of the LockLens REST API (internal/api). Nullable fields mean
// "unknown or unavailable" and must never be rendered as zero.

export type TargetState = 'pending' | 'healthy' | 'degraded' | 'unreachable' | 'disabled'

export interface Capabilities {
  server_version: string
  server_version_num: number
  is_standby: boolean
  read_all_stats: boolean
  track_activities: boolean
  track_io_timing: boolean
  pg_stat_io: boolean
  pg_stat_checkpointer: boolean
  pg_stat_statements: boolean
  pg_stat_statements_reason?: string
  pg_stat_statements_version?: string
  detected_at: string
}

export interface CollectorStatus {
  name: string
  interval_seconds: number
  enabled: boolean
  disabled_reason?: string
  last_attempt: string | null
  last_success: string | null
  last_error: string | null
  last_duration_ms: number | null
  consecutive_failures: number
}

export interface TargetStatus {
  state: TargetState
  server_version: string | null
  is_standby: boolean | null
  capabilities: Capabilities | null
  collectors: Record<string, CollectorStatus>
  last_attempt_at: string | null
  last_success_at: string | null
  last_error: string | null
  last_error_at: string | null
  freshness_seconds: number | null
}

export interface Target {
  id: number
  name: string
  description: string
  host: string
  port: number
  database: string
  user: string
  enabled: boolean
  interval_seconds: number
  query_text_enabled: boolean
  max_connections: number
  node_exporter_url: string | null
  created_at: string
  updated_at: string
  status: TargetStatus
}

export interface TargetInput {
  name: string
  description: string
  dsn: string
  enabled: boolean
  interval_seconds: number
  query_text_enabled: boolean
  max_connections: number
  node_exporter_url: string | null
}

export interface LatestValue { at: string; value: number | null }

export interface TargetCounts {
  open_incidents: number
  critical_findings: number
  warning_findings: number
  info_findings: number
}

export interface FleetEntry {
  target: Target
  latest: Record<string, LatestValue>
  counts: TargetCounts
}

export interface Session {
  username: string
  csrf_token: string
  expires_at: string
  server: { version: string; demo_mode: boolean; time: string }
}

export interface Evidence { label: string; value: unknown; unit?: string; source: string; at?: string }

export type Severity = 'info' | 'warning' | 'critical'

export interface Finding {
  rule_id: string
  rule_version: number
  key: string
  severity: Severity
  title: string
  status: 'active' | 'insufficient_data' | 'resolved'
  observed_from: string
  observed_to: string
  observed: string[]
  evidence: Evidence[]
  explanation: string
  possible_explanations: string[]
  next_checks: string[]
  limitations: string
  trigger?: boolean
}

export interface FindingRow {
  id: number
  target_id: number
  incident_id: number | null
  first_seen: string
  last_seen: string
  resolved_at: string | null
  finding: Finding
}

export interface Incident {
  id: number
  target_id: number | null
  target_name: string
  rule_id: string
  rule_version: number
  severity: Severity
  title: string
  state: 'open' | 'resolved'
  opened_at: string
  last_seen_at: string
  resolved_at: string | null
  resolution: string | null
  trigger_config: unknown
  acknowledged_at: string | null
  acknowledged_by: string | null
  ack_note: string | null
  event_count: number
  size_bytes: number
  truncated: boolean
  source: 'recorder' | 'import'
}

export interface IncidentEvent { id: number; at: string; kind: string; summary: string; payload: Record<string, unknown> }

export interface SeriesPoint { t: string; v: number | null; n: number; known: number }
export interface Series { metric: string; label?: string; aggregation: string; points: SeriesPoint[] }
export interface Gap { collector: string; from: string; to: string; reason: string }
export interface MetricsResponse { from: string; to: string; step_seconds: number; series: Series[]; gaps: Gap[] }

// Blocking report (internal/report).
export interface WaitingLock {
  pid: number
  locktype: string
  mode: string
  database: string | null
  relation_oid: number | null
  relation: string | null
  page: number | null
  tuple: number | null
  virtualxid: string | null
  transactionid: string | null
  classid: number | null
  objid: number | null
  objsubid: number | null
  wait_start: string | null
}

export interface SnapshotSession {
  pid: number
  leader_pid: number | null
  database: string | null
  user: string | null
  application_name: string | null
  client_addr: string | null
  backend_type: string | null
  state: string | null
  wait_event_type: string | null
  wait_event: string | null
  backend_start: string | null
  xact_start: string | null
  query_start: string | null
  state_change: string | null
  backend_xid: string | null
  query?: string
  query_truncated?: boolean
  details_visible: boolean
  parallel_workers: number
  waiting_locks: WaitingLock[]
}

export interface Warning { code: string; message: string }

export interface ReportNode {
  id: number
  kind: 'session' | 'prepared_transaction' | 'missing'
  blocked_by: number[]
  blocks: number[]
  direct_blocked_count: number
  transitive_blocked_count: number
  root: boolean
  in_cycle: boolean
  component: number
}

export interface ReportComponent { id: number; members: number[]; roots: number[]; cyclic: boolean; cycle_members: number[]; entries: number[] }

export interface SessionAges {
  pid: number
  transaction_age_seconds: number | null
  query_age_seconds: number | null
  state_age_seconds: number | null
  lock_wait_seconds: number | null
}

export interface LongTransaction { pid: number; state: string | null; transaction_age_seconds: number; idle_in_transaction: boolean; direct_blocked_count: number }

export interface Report {
  snapshot: {
    schema_version: number
    collected_at: string
    server_time: string
    collection: { duration_ms: number; include_query: boolean; query_max_length: number; min_tx_age_seconds: number; atomic: boolean }
    server: { host: string; port: number; database: string; user: string; version: string; version_num: number; read_all_stats: boolean; track_activities: boolean }
    sessions: SnapshotSession[]
    edges: { blocker: number; waiter: number }[]
    prepared_transactions: { gid: string; transaction: string; prepared: string; owner: string; database: string }[]
    warnings: Warning[]
  }
  analysis: {
    min_tx_age_seconds: number
    summary: { blocked_sessions: number; blocking_sessions: number; root_blockers: number; cyclic_components: number; long_transactions: number; idle_in_transaction: number }
    nodes: ReportNode[]
    edges: { blocker: number; waiter: number }[]
    components: ReportComponent[]
    sessions: SessionAges[]
    long_transactions: LongTransaction[]
    warnings: Warning[]
  }
}

export interface ActivityCounts {
  client_total: number
  active: number
  idle: number
  idle_in_transaction: number
  idle_in_transaction_aborted: number
  unknown_state: number
  waiting_lock: number
  used_connections: number
  max_connections: number
  reserved_connections: number
}

export interface VacuumProgress {
  pid: number
  datname: string | null
  relation: string | null
  phase: string | null
  heap_blks_total: number | null
  heap_blks_scanned: number | null
  heap_blks_vacuumed: number | null
  index_vacuum_count: number | null
}

export interface ActivityResponse {
  source: 'live' | 'stored' | 'none' | 'upload'
  observed_at?: string
  report: Report | null
  counts?: ActivityCounts
  vacuum_progress?: VacuumProgress[]
  connected?: boolean
  snapshot_id?: number
  query_text_enabled?: boolean
}

export interface Comparison {
  mean_ratio: number | null
  calls_ratio: number | null
  total_time_ratio: number | null
  mean_regression: boolean
  volume_increase: boolean
  load_increase: boolean
  too_few_calls: boolean
}

export interface QueryItem {
  identity_id: number
  server_major: number
  datname: string
  username: string
  toplevel: boolean
  /** 64-bit identifier, sent as a string to keep every digit. */
  queryid: string
  query_text: string | null
  calls: number
  total_exec_ms: number
  rows: number
  shared_blks_hit: number
  shared_blks_read: number
  temp_blks_written: number
  wal_bytes: number | null
  share_of_total_time: number | null
  previous_calls: number | null
  previous_total_exec_ms: number | null
  mean_exec_ms: number | null
  calls_per_second: number | null
  previous_mean_exec_ms: number | null
  comparison: Comparison | null
}

export interface Coverage { window_seconds: number; covered_seconds: number; collections: number; resets: number; evicted_entries: number; hidden_entries_max: number }

export interface QueriesResponse {
  from: string
  to: string
  items: QueryItem[]
  total: number
  page_size: number
  coverage: Coverage
  previous_coverage?: Coverage
  query_text_enabled: boolean
  text_included: boolean
  min_calls: number
  monitoring_user: string
  monitoring_hidden: boolean
  available?: boolean
  unavailable_reason?: string
}

export interface WindowTotals { calls: number; total_exec_ms: number; rows: number }

export interface QueryDetailResponse {
  identity: QueryItem
  first_seen: string
  last_seen: string
  from: string
  to: string
  step_seconds: number
  buckets: { t: string; calls: number; total_exec_ms: number; rows: number; covered_seconds: number }[]
  query_text_enabled: boolean
  text_included: boolean
  current: { from: string; to: string; totals: WindowTotals; mean_exec_ms: number | null; coverage: Coverage }
  previous: { from: string; to: string; totals: WindowTotals; mean_exec_ms: number | null; coverage: Coverage }
  comparison: Comparison
}

export interface TableStat {
  relid: number
  datname: string
  schema: string
  name: string
  n_live_tup: number | null
  n_dead_tup: number | null
  n_mod_since_analyze: number | null
  last_vacuum: string | null
  last_autovacuum: string | null
  last_analyze: string | null
  last_autoanalyze: string | null
  table_bytes: number | null
  indexes_bytes: number | null
  total_bytes: number | null
  frozenxid_age: number | null
}

export interface IndexStat {
  indexrelid: number
  relid: number
  schema: string
  table: string
  name: string
  idx_scan: number | null
  index_bytes: number | null
  is_unique: boolean
  is_primary: boolean
  is_constraint: boolean
}

export interface TableHealth {
  at: string
  datname: string
  size_source: 'exact' | 'estimate'
  tables: TableStat[]
  indexes: IndexStat[]
  databases: { datname: string; xid_age: number | null; mxid_age: number | null; size_bytes: number | null }[]
  settings: Record<string, string>
  warnings: string[]
}

export interface TablesResponse {
  source: 'live' | 'stored' | 'none'
  tables: TableHealth | null
  vacuum_progress?: VacuumProgress[]
  vacuum_progress_at?: string
  long_transactions?: LongTransaction[]
}

export interface Replica {
  pid: number
  usename: string | null
  application_name: string
  client_addr: string | null
  state: string | null
  sync_state: string | null
  sent_lsn: string | null
  write_lsn: string | null
  flush_lsn: string | null
  replay_lsn: string | null
  sent_lag_bytes: number | null
  write_lag_bytes: number | null
  flush_lag_bytes: number | null
  replay_lag_bytes: number | null
  write_lag_seconds: number | null
  flush_lag_seconds: number | null
  replay_lag_seconds: number | null
  reply_time: string | null
}

export interface Slot {
  slot_name: string
  slot_type: string
  active: boolean
  wal_status: string | null
  restart_lsn: string | null
  retained_bytes: number | null
  safe_wal_size: number | null
  inactive_since: string | null
}

export interface Replication {
  at: string
  role: 'primary' | 'standby'
  current_lsn: string | null
  replicas: Replica[]
  slots: Slot[]
  standby: {
    receive_lsn: string | null
    replay_lsn: string | null
    replay_delay_bytes: number | null
    last_xact_replay_timestamp: string | null
    replay_pause_state: string | null
    wal_receiver_status: string | null
    sender_host: string | null
    slot_name: string | null
    last_msg_receipt_time: string | null
  } | null
  archiver: { archive_mode: string; archived_count: number; failed_count: number; last_archived_time: string | null; last_failed_time: string | null; last_failed_wal: string | null } | null
  warnings: string[]
}

export interface HostResponse {
  configured: boolean
  available: boolean
  error?: string | null
  host: {
    at: string
    cpu_utilization_ratio: number | null
    memory_total_bytes: number | null
    memory_available_bytes: number | null
    filesystems: { mountpoint: string; fstype: string; size_bytes: number; avail_bytes: number }[] | null
    disk_read_bytes_per_second: number | null
    disk_write_bytes_per_second: number | null
    network_receive_bytes_per_second: number | null
    network_transmit_bytes_per_second: number | null
  } | null
}

export type Thresholds = Record<string, number>
