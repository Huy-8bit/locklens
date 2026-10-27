// Types of the LockLens REST API (internal/transport/httpapi). Nullable fields mean
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
  environment: string
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
  environment: string
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

export type Role = 'admin' | 'operator' | 'viewer'

export type Permission = 'view' | 'workspace.structure' | 'workspace.data' | 'workspace.sql' | 'incidents.acknowledge' | 'targets.manage' | 'settings.manage' | 'users.manage'

export interface User {
  id: number
  username: string
  role: Role
  disabled: boolean
  /** The administrator configured by the environment; changed only there. */
  managed: boolean
  created_at: string
  updated_at: string
  last_login_at: string | null
}

/** What adding a connection string would do (POST /targets/inspect). */
export interface SetupPlan {
  ok: boolean
  error: string | null
  hint: string | null
  mode: 'direct' | 'create_roles' | 'reuse_roles' | 'unsupported' | ''
  reason: string
  host: string
  port: number
  database: string
  user: string
  server_version: string
  is_standby: boolean
  superuser: boolean
  sees_all_sessions: boolean
  workspace_allowed: boolean
  pg_stat_statements: boolean
  suggested_name: string
  statements: string[]
  existing_roles: string[]
  warnings: string[]
}

export interface Session {
  user_id: number
  role: Role
  managed: boolean
  permissions: Permission[]
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
  /** How strongly the data supports the finding (statement rules). */
  confidence?: Confidence
  /** Machine-readable measurements behind the finding. */
  metrics?: Record<string, number>
}

export interface Confidence { level: 'high' | 'medium' | 'low'; reasons: string[] }

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

/** pg_stat_statements identity key; queryid is a string to keep every digit. */
export interface QueryKey { server_major: number; dbid: number; userid: number; toplevel: boolean; queryid: string }

/** A statement named by an incident, resolved to its stored identity. */
export interface QueryRef { key: QueryKey; identity_id: number; datname: string; username: string; command: string; query_text: string | null }

export interface IncidentEventsResponse { events: IncidentEvent[]; has_more: boolean; statements: QueryRef[] }

/** One statement of a workload summary event (top statements by total time). */
export interface WorkloadEntry { key: QueryKey; datname: string; username: string; text?: string; calls: number; total_exec_ms: number; rows: number; mean_exec_ms: number | null; share_of_total_time: number | null }

/** One client session in a "sessions" event (recorded when an incident opens). */
export interface SessionBrief {
  pid: number
  database: string | null
  user: string | null
  application_name: string | null
  client_addr: string | null
  state: string | null
  wait_event_type: string | null
  wait_event: string | null
  query_age_seconds: number | null
  transaction_age_seconds: number | null
  blocked_by?: number[]
  blocks?: number
  query?: string
  query_truncated?: boolean
}

export interface SeriesPoint { gap?: boolean; t: string; v: number | null; n: number; known: number }
export interface Series { last_sample_at?: string; collector?: string; metric: string; label?: string; aggregation: string; points: SeriesPoint[] }
export interface Gap { collector: string; from: string; to: string; reason: string }
export interface MetricsResponse { collectors?: Record<string, CollectorStatus>; annotations?: { id: number; title: string; opened_at: string; resolved_at?: string | null; severity: string }[]; annotations_truncated?: boolean; gaps_truncated?: boolean; collection_interval_seconds?: number; from: string; to: string; step_seconds: number; series: Series[]; gaps: Gap[] }

// Blocking report (internal/domain/report).
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
  /** Statement class: select, insert, update, delete, merge, copy, ddl, transaction, maintenance, utility, other. */
  command: string
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
  buckets: { gap?: boolean; t: string; calls: number; total_exec_ms: number; rows: number; covered_seconds: number }[]
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

export interface CommandTotals { command: string; calls: number; total_exec_ms: number; rows: number; statements: number; mean_exec_ms: number | null }
export interface CommandBucket { t: string; command: string; calls: number; total_exec_ms: number; rows: number }
export interface CommandsResponse {
  from: string
  to: string
  step_seconds: number
  commands: string[]
  totals: CommandTotals[]
  buckets: CommandBucket[]
  /** Collection time per bucket: covered with no rows means zero activity; absent means unknown. */
  coverage_buckets: { t: string; covered_seconds: number }[]
  coverage: Coverage
  monitoring_user: string
  monitoring_hidden: boolean
  available?: boolean
  unavailable_reason?: string
}

export interface OverviewResponse {
  target: Target
  latest: Record<string, LatestValue>
  connected: boolean
  connect_error?: string | null
  activity_at?: string
  counts?: ActivityCounts
  summary?: Report['analysis']['summary']
  visibility_warnings?: Warning[]
  host_configured?: boolean
  host_error?: string | null
  findings: FindingRow[]
  open_incidents: Incident[]
  open_incident_count: number
}

export interface TestResult {
  ok: boolean
  duration_ms: number
  target: string
  capabilities: Capabilities | null
  checks: { name: string; status: string; detail?: string }[]
  error: string | null
  hint: string | null
}

export interface WorkspacePolicy {
  target_id: number
  metadata_enabled: boolean
  data_enabled: boolean
  sql_enabled: boolean
  history_enabled: boolean
  configured: boolean
  updated_at: string
}

/** A bounded result from the workspace (catalog sections and table data). All values are text or null. */
export interface GridResult {
  columns: { name: string; type_oid: number }[]
  rows: (string | null)[][]
  truncated: boolean
  cell_truncated: boolean
  bytes: number
  duration_ms: number
  pagination?: string
  next?: string
}

export interface FleetPoint { t: string; v: number | null; targets: number }
export interface FleetMetricsResponse { from: string; to: string; step_seconds: number; targets: number; series: { metric: string; aggregation: string; points: FleetPoint[] }[] }

export interface FleetQuery extends QueryItem { target_id: number; target_name: string }
export interface FleetQueriesResponse { from: string; to: string; sort: string; items: FleetQuery[]; targets: number }

export interface ConnectionGroup { database: string; user: string; application: string; client_addr: string; state: string; count: number; oldest_seconds: number | null }

export interface FleetLiveTarget {
  target: Target
  connected: boolean
  observed_at: string | null
  latest: Record<string, LatestValue>
  counts: ActivityCounts | null
  report: Report | null
  replication: Replication | null
  connection_groups: ConnectionGroup[]
  host: HostResponse['host']
}
export interface FleetLiveResponse { targets: FleetLiveTarget[]; generated_at: string }

export interface SystemResponse {
  version: string
  demo_mode: boolean
  time: string
  store: { reachable: boolean; schema_version: number }
  writer: { queue_length: number; queue_capacity: number; dropped_batches: number; write_errors: number }
  targets: { total: number; by_state: Record<string, number> }
  collectors: { enabled: number; failing: number }
  incidents: { open: number; critical: number }
}

/** Database load (active session sampling) of one target, grouped by one dimension. */
export type LoadDimension = 'class' | 'event' | 'query' | 'user' | 'database' | 'application' | 'backend'

export interface LoadGroup {
  key: string
  /** Dimension values; for statements: dbid, userid, queryid, database, user. */
  values: string[]
  label: string
  /** Average active sessions over the window. */
  aas: number
  share: number | null
  /** AAS by wait class. */
  classes: Record<string, number>
}

export interface LoadResponse {
  from: string
  to: string
  step_seconds: number
  load: {
    dimension: LoadDimension
    ticks: number
    active_aas: number | null
    idle_in_transaction_aas: number | null
    series: { key: string; label: string; points: { t: string; v: number | null }[] }[]
    top: LoadGroup[]
  }
  statements: QueryRef[]
  query_text_enabled: boolean
  collector: CollectorStatus | null
}

/** Incident notifications. Destinations are write-only: the API returns only `display`. */
/** How the monitoring store keeps history (GET /api/v1/storage). */
export interface StoreTimescale {
  enabled: boolean
  version?: string
  license?: string
  compression: boolean
  compress_after_seconds?: number
  reason?: string
  hypertables?: string[]
}

export interface StoreTable { table: string; bytes: number; estimated_rows: number; compressed_before_bytes?: number; compressed_after_bytes?: number }

export type ChannelKind = 'webhook' | 'slack' | 'email'

export interface NotificationChannel {
  id: number
  name: string
  kind: ChannelKind
  enabled: boolean
  min_severity: Severity
  environments: string[]
  on_open: boolean
  on_escalate: boolean
  on_resolve: boolean
  reminder_seconds: number
  display: string
  created_at: string
  updated_at: string
}

export interface ChannelDestination {
  url?: string
  secret?: string
  smtp_host?: string
  smtp_port?: number
  tls?: 'tls' | 'starttls' | 'none'
  username?: string
  password?: string
  from?: string
  to?: string[]
}

export interface ChannelInput {
  name: string
  kind: ChannelKind
  enabled: boolean
  min_severity: Severity
  environments: string[]
  on_open: boolean
  on_escalate: boolean
  on_resolve: boolean
  reminder_minutes: number
  /** Omit on update to keep the stored destination. */
  destination?: ChannelDestination
}

export interface Delivery {
  id: number
  channel_id: number
  channel_name: string
  incident_id: number | null
  event: 'opened' | 'escalated' | 'resolved' | 'reminder' | 'test'
  severity: string | null
  status: 'sent' | 'failed' | 'silenced'
  attempts: number
  error: string | null
  at: string
}

export interface Silence {
  id: number
  target_id: number | null
  target_name: string | null
  rule_id: string | null
  starts_at: string
  ends_at: string
  reason: string
  created_by: string
  created_at: string
}

// Query performance intelligence

export interface ImpactComponent { name: string; points: number; max: number; detail: string }
export interface Impact { score: number; components: ImpactComponent[]; confidence_factor: number }

/** A current statement finding ranked by impact. */
export interface PerfItem {
  target_id: number
  target_name: string
  finding_id: number
  first_seen: string
  last_seen: string
  finding: Finding
  impact: Impact
  statement: QueryRef | null
}

export interface PercentilesNote { available: false; reason: string }

export interface PerformanceResponse {
  generated_at: string
  anomalies: PerfItem[]
  running: PerfItem[]
  unavailable: { target_id: number; target_name: string; reason: string }[]
  insufficient: { target_id: number; target_name: string; rule_id: string; reason: string }[]
  baselines: { target_id: number; target_name: string; statements: number; warm: number }[]
  baseline_config: { days: number; point_seconds: number; warmup_points: number; sensitivity: number }
  percentiles: PercentilesNote
}

export interface BaselineStat { median: number; mad: number; n: number }
export interface BaselineProfile { points: number; stats: Partial<Record<string, BaselineStat>> }
export interface BandPoint { t: string; median: number; low: number; high: number; basis: string }

export interface Hypothesis {
  id: string
  title: string
  score: number
  strength: 'strong' | 'moderate' | 'weak'
  supporting: string[]
  contradicting: string[]
  next_steps: string[]
}

export interface Investigation {
  at: string
  since: string
  summary: string
  hypotheses: Hypothesis[]
  timeline: { at: string; kind: 'anomaly' | 'finding' | 'gap'; rule_id?: string; summary: string }[]
  missing_evidence: string[]
  disclaimer: string
}

export interface QueryInsightResponse {
  baseline: {
    from: string
    to: string
    point_seconds: number
    points: number
    days: number
    warm: boolean
    warmup_points: number
    all: BaselineProfile
    current: BaselineProfile
    current_basis: string
    sensitivity: number
  }
  bands: { mean_ms: BandPoint[]; calls_per_s: BandPoint[]; load_ms_per_s: BandPoint[] }
  step_seconds: number
  percentiles: PercentilesNote
  anomalies: PerfItem[]
  history: PerfItem[]
  investigation?: Investigation
  plans: number
}

export interface IncidentInvestigationResponse { available: boolean; reason?: string; investigation?: Investigation }

export interface PlanRow {
  id: number
  identity_id: number
  captured_at: string
  captured_by: string
  source: 'workspace_explain' | 'generic_plan' | 'manual'
  format: 'json' | 'text'
  plan?: string
  fingerprint: string
  total_cost: number | null
  note: string
}

export interface PlanNode { depth: number; node_type: string; relation?: string; index?: string; join_type?: string; strategy?: string; total_cost: number; plan_rows: number; filter?: string; cond?: string; actual_total_time?: number }
export interface ParsedPlan { format: 'json' | 'text'; nodes: PlanNode[]; total_cost: number | null; fingerprint: string; analyzed: boolean }
export interface IndexHint { relation: string; columns: string[]; reason: string }
export interface PlanDetailResponse { plan: PlanRow; parsed: ParsedPlan; index_hints: IndexHint[]; index_hints_note: string }
export interface PlanCompareResponse {
  a: PlanRow
  b: PlanRow
  parsed_a: ParsedPlan
  parsed_b: ParsedPlan
  comparison: { same_shape: boolean; cost_ratio: number | null; changes: { kind: string; detail: string }[] }
}
