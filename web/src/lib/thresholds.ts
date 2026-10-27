/**
 * How the rule thresholds are shown and edited: grouped by the rule that uses
 * them, with a label, a unit people think in, and what the value changes.
 * Values are stored in the API's units (seconds, bytes, ratios 0–1) and
 * converted for display.
 */
export type ThresholdUnit = 'seconds' | 'ms' | 'bytes' | 'percent' | 'times' | 'count' | 'samples'

export interface ThresholdField {
  key: string
  label: string
  unit: ThresholdUnit
  help?: string
}

export interface ThresholdGroup {
  id: string
  title: string
  description: string
  fields: ThresholdField[]
}

export const THRESHOLD_GROUPS: ThresholdGroup[] = [
  {
    id: 'blocking',
    title: 'Blocking',
    description: 'Sessions waiting for locks held by another session.',
    fields: [
      { key: 'blocking_min_seconds', label: 'Report a blocking chain after', unit: 'seconds', help: 'How long sessions must wait before it is a finding.' },
      { key: 'blocking_min_samples', label: '…seen in at least', unit: 'samples', help: 'Consecutive collections, so a brief wait is ignored.' },
      { key: 'blocking_min_blocked', label: '…with at least', unit: 'count', help: 'Waiting sessions behind one root blocker.' },
      { key: 'blocking_critical_blocked', label: 'Critical when this many wait', unit: 'count' },
      { key: 'blocking_critical_seconds', label: 'Critical after waiting', unit: 'seconds' },
      { key: 'idle_blocker_min_samples', label: 'Idle-in-transaction blocker after', unit: 'samples', help: 'A blocker that is idle in its transaction is always critical.' },
    ],
  },
  {
    id: 'connections',
    title: 'Connections',
    description: 'Used connections compared with max_connections (minus reserved slots).',
    fields: [
      { key: 'connections_warn_ratio', label: 'Warning at', unit: 'percent' },
      { key: 'connections_critical_ratio', label: 'Critical at', unit: 'percent' },
      { key: 'connections_min_samples', label: 'For at least', unit: 'samples' },
    ],
  },
  {
    id: 'transactions',
    title: 'Long transactions',
    description: 'Transactions open for a long time hold locks and keep vacuum from cleaning up.',
    fields: [
      { key: 'long_transaction_warn_seconds', label: 'Warning after', unit: 'seconds' },
      { key: 'long_transaction_critical_seconds', label: 'Critical after', unit: 'seconds' },
    ],
  },
  {
    id: 'queries',
    title: 'Query performance',
    description: 'Statements compared with their baseline, or with the previous equal window while the baseline is still learning.',
    fields: [
      { key: 'workload_window_seconds', label: 'Compare windows of', unit: 'seconds' },
      { key: 'workload_min_coverage', label: 'Each window collected for at least', unit: 'percent', help: 'Less data than this gives "not evaluated", never a pass.' },
      { key: 'regression_mean_ratio', label: 'A statement is slower when its time per call grows', unit: 'times' },
      { key: 'regression_min_delta_ms', label: '…and by at least', unit: 'ms' },
      { key: 'regression_min_calls', label: 'Calls needed in each window', unit: 'count' },
      { key: 'volume_calls_ratio', label: 'Report a call volume increase of', unit: 'times' },
      { key: 'volume_min_calls', label: '…with at least this many calls', unit: 'count' },
    ],
  },
  {
    id: 'slow_queries',
    title: 'Slow running queries',
    description: 'Statements still executing, from the latest activity snapshot. A critical one opens an incident.',
    fields: [
      { key: 'slow_query_warn_seconds', label: 'Warning when a statement has run for', unit: 'seconds' },
      { key: 'slow_query_critical_seconds', label: 'Critical when it has run for', unit: 'seconds' },
      { key: 'slow_query_expected_factor', label: 'Also warn at this multiple of its usual time per call', unit: 'times', help: 'Needs a learned baseline for the statement (pg_stat_activity.query_id).' },
      { key: 'slow_query_expected_min_seconds', label: '…but never before', unit: 'seconds' },
    ],
  },
  {
    id: 'baselines',
    title: 'Baselines and anomalies',
    description: 'Each statement is compared with its own history (median and median absolute deviation), by hour of the day when there is enough history.',
    fields: [
      { key: 'baseline_days', label: 'Learn from the last', unit: 'count', help: 'Days of history.' },
      { key: 'baseline_point_seconds', label: 'One baseline point every', unit: 'seconds' },
      { key: 'baseline_warmup_points', label: 'Points needed before a baseline is used', unit: 'count', help: 'Until then statements are compared with the previous window only.' },
      { key: 'baseline_min_calls_per_point', label: 'Calls needed in a point for per-call values', unit: 'count' },
      { key: 'anomaly_sensitivity', label: 'Anomaly at this many robust deviations', unit: 'times', help: 'Higher is less sensitive.' },
      { key: 'regression_critical_ratio', label: 'A slower statement is critical at', unit: 'times', help: 'Its usual time per call.' },
      { key: 'load_ratio', label: 'Load increase at', unit: 'times', help: 'Its usual execution time per second.' },
      { key: 'load_min_added_ms_per_s', label: '…adding at least', unit: 'ms', help: 'Milliseconds of execution per second.' },
      { key: 'io_ratio', label: 'I/O regression at', unit: 'times', help: 'Its usual blocks read per call.' },
      { key: 'io_min_reads_per_call', label: '…reading at least', unit: 'count', help: 'Blocks per call.' },
      { key: 'pattern_rows_ratio', label: 'Rows per call change of', unit: 'times' },
      { key: 'n_plus_one_min_calls_per_s', label: 'Possible N+1 at', unit: 'count', help: 'Calls per second of a fast single-row lookup.' },
      { key: 'n_plus_one_max_mean_ms', label: '…taking at most', unit: 'ms', help: 'Per call.' },
      { key: 'n_plus_one_max_rows_per_call', label: '…returning at most', unit: 'count', help: 'Rows per call.' },
    ],
  },
  {
    id: 'temp',
    title: 'Temporary files',
    description: 'Sorts and hashes spilling to disk because work_mem is too small for them.',
    fields: [
      { key: 'temp_window_seconds', label: 'Compare windows of', unit: 'seconds' },
      { key: 'temp_ratio', label: 'Report an increase of', unit: 'times' },
      { key: 'temp_min_bytes', label: '…writing at least', unit: 'bytes' },
    ],
  },
  {
    id: 'deadlocks',
    title: 'Deadlocks',
    description: 'Deadlocks PostgreSQL detected and resolved by cancelling a transaction.',
    fields: [{ key: 'deadlock_window_seconds', label: 'Report deadlocks within', unit: 'seconds' }],
  },
  {
    id: 'vacuum',
    title: 'Vacuum',
    description: 'Dead rows that vacuum has not removed, and transaction ID age (wraparound).',
    fields: [
      { key: 'dead_tuples_min', label: 'A table needs at least', unit: 'count', help: 'Dead rows.' },
      { key: 'dead_tuples_ratio', label: '…and dead rows above', unit: 'percent', help: 'Of the table’s rows.' },
      { key: 'dead_tuples_vacuum_age_seconds', label: '…without a vacuum for', unit: 'seconds' },
      { key: 'xid_age_warn_fraction', label: 'Transaction ID age warning at', unit: 'percent', help: 'Of the wraparound limit.' },
      { key: 'xid_age_critical_fraction', label: 'Transaction ID age critical at', unit: 'percent' },
    ],
  },
  {
    id: 'replication',
    title: 'Replication and WAL',
    description: 'Standbys falling behind, replication slots retaining WAL, archiving failures.',
    fields: [
      { key: 'replica_lag_bytes', label: 'Replica lag above', unit: 'bytes' },
      { key: 'replica_lag_min_samples', label: '…for at least', unit: 'samples' },
      { key: 'slot_retained_bytes', label: 'A slot retaining more WAL than', unit: 'bytes' },
      { key: 'archiver_window_seconds', label: 'Report archiver failures within', unit: 'seconds' },
    ],
  },
]

const MIB = 1024 * 1024

/** The value as edited in the form. */
export function toDisplay(unit: ThresholdUnit, v: number): number {
  switch (unit) {
    case 'bytes':
      return Math.round((v / MIB) * 100) / 100
    case 'percent':
      return Math.round(v * 10000) / 100
  }
  return v
}

/** The value as stored by the API. */
export function fromDisplay(unit: ThresholdUnit, v: number): number {
  switch (unit) {
    case 'bytes':
      return Math.round(v * MIB)
    case 'percent':
      return v / 100
  }
  return v
}

export const UNIT_SUFFIX: Record<ThresholdUnit, string> = {
  seconds: 's',
  ms: 'ms',
  bytes: 'MiB',
  percent: '%',
  times: '×',
  count: '',
  samples: 'samples',
}

/** Keys the API returns that no group describes (shown under "Other"). */
export function ungrouped(keys: string[]): string[] {
  const known = new Set(THRESHOLD_GROUPS.flatMap((g) => g.fields.map((f) => f.key)))
  return keys.filter((k) => !known.has(k)).sort()
}
