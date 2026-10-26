import type { LoadDimension } from '../api/types'

/**
 * Wait classes of active session sampling, in display order. Each class keeps
 * its color everywhere (it follows the class, never its rank).
 */
export const WAIT_CLASSES: { key: string; label: string; color: string; hint: string }[] = [
  { key: 'CPU', label: 'CPU (not waiting)', color: 'var(--s3)', hint: 'Running, or waiting on something PostgreSQL does not report as a wait event' },
  { key: 'IO', label: 'I/O', color: 'var(--s1)', hint: 'Reading or writing data files, WAL, or temporary files' },
  { key: 'Lock', label: 'Lock', color: 'var(--s2)', hint: 'Waiting for a heavyweight lock held by another session (rows, tables, transactions)' },
  { key: 'LWLock', label: 'LWLock', color: 'var(--s7)', hint: 'Waiting for an internal lock (buffer mapping, WAL insertion, …)' },
  { key: 'IPC', label: 'IPC', color: 'var(--s5)', hint: 'Waiting for another process (parallel workers, replication, …)' },
  { key: 'Client', label: 'Client', color: 'var(--s4)', hint: 'Waiting for the client to send a request or read a result' },
  { key: 'BufferPin', label: 'Buffer pin', color: 'var(--s-other)', hint: 'Waiting for exclusive access to a data buffer' },
  { key: 'Timeout', label: 'Timeout', color: 'var(--ink-2)', hint: 'Sleeping until a timeout: pg_sleep, vacuum or commit delays, recovery pauses' },
  { key: 'Activity', label: 'Activity', color: 'var(--s-other)', hint: 'A background process waiting for work (usually not client load)' },
  { key: 'Extension', label: 'Extension', color: 'var(--s-other)', hint: 'Waiting inside an extension' },
  { key: 'IdleTx', label: 'Idle in transaction', color: 'var(--s-other)', hint: 'Not running anything but holding a transaction open (not counted as load)' },
]

const OTHER_CLASS_COLOR = 'var(--s-other)'

export function classInfo(key: string) {
  return WAIT_CLASSES.find((c) => c.key === key) ?? { key, label: key, color: OTHER_CLASS_COLOR, hint: 'Other wait event type reported by PostgreSQL' }
}

export const LOAD_DIMENSIONS: { value: LoadDimension; label: string }[] = [
  { value: 'class', label: 'Wait class' },
  { value: 'event', label: 'Wait event' },
  { value: 'query', label: 'Statement' },
  { value: 'user', label: 'User' },
  { value: 'database', label: 'Database' },
  { value: 'application', label: 'Application' },
  { value: 'backend', label: 'Backend type' },
]

/** The statement key (dbid/userid/queryid) of a statement group's values. */
export function statementKey(values: string[]): string {
  return values.slice(0, 3).join('/')
}
