import type { Finding, Incident, IncidentEvent, QueryKey } from '../api/types'

/** A statement identity as one string: server major, dbid, userid, toplevel, queryid. */
export function queryKey(k: QueryKey): string {
  return `${k.server_major}:${k.dbid}:${k.userid}:${k.toplevel}:${k.queryid}`
}

/** The statement key of a statement finding ("q:<key>"), or null. */
export function findingQueryKey(f: Finding | undefined): string | null {
  return f?.key.startsWith('q:') ? f.key.slice(2) : null
}

/** An evidence value by label. */
export function evidence(f: Finding | undefined, label: string): unknown {
  return f?.evidence.find((e) => e.label === label)?.value
}

export function evidenceNumber(f: Finding | undefined, label: string): number | null {
  const v = evidence(f, label)
  return typeof v === 'number' && Number.isFinite(v) ? v : null
}

export type IncidentFocus = 'statement' | 'blocking' | 'connections' | 'replication' | 'other'

/** What each trigger rule watches for, in words an on-call engineer can act on. */
export const RULE_INFO: Record<string, { name: string; focus: IncidentFocus; what: string }> = {
  'queries.mean_regression': {
    name: 'A statement became slower',
    focus: 'statement',
    what: 'Each call of one statement takes much longer than in the window before. LockLens compares the average execution time per call over two equal windows of pg_stat_statements data; the statement may run as often as before, but every run now costs more.',
  },
  'blocking.long_chain': {
    name: 'Sessions are stuck waiting for locks',
    focus: 'blocking',
    what: 'One session holds locks that other sessions wait for, longer than the configured threshold. To the application this looks like hung requests; the root blocker is the session to look at first.',
  },
  'blocking.idle_in_tx_blocker': {
    name: 'An idle transaction is blocking others',
    focus: 'blocking',
    what: 'A session that is idle inside an open transaction holds locks that other sessions wait for. It does nothing until its application commits or rolls back, so the waiters stay stuck.',
  },
  'connections.pressure': {
    name: 'Connections are running out',
    focus: 'connections',
    what: 'Used connections are close to max_connections. When the limit is reached, PostgreSQL refuses new connections and applications fail to connect.',
  },
  'replication.slot_retained_wal': {
    name: 'A replication slot is retaining WAL',
    focus: 'replication',
    what: 'A replication slot keeps WAL on the primary because its consumer is behind or gone. Disk usage grows until the consumer catches up or the slot is removed.',
  },
}

export function ruleInfo(ruleId: string) {
  return RULE_INFO[ruleId] ?? { name: ruleId, focus: 'other' as IncidentFocus, what: '' }
}

/** Kinds of routine evidence that repeat while an incident is recorded. */
const ROUTINE = new Set(['pre_trigger', 'sample', 'post_trigger', 'workload'])

export type TimelineRow = { type: 'event'; event: IncidentEvent } | { type: 'group'; kind: string; events: IncidentEvent[] }

/**
 * Collapses runs of routine evidence with the same kind and summary (for
 * example thirty identical samples before the trigger) into one row, so the
 * events that matter stand out.
 */
export function groupTimeline(events: IncidentEvent[]): TimelineRow[] {
  const out: TimelineRow[] = []
  for (const ev of events) {
    const last = out[out.length - 1]
    if (ROUTINE.has(ev.kind) && last) {
      if (last.type === 'group' && last.kind === ev.kind && last.events[0]?.summary === ev.summary) {
        last.events.push(ev)
        continue
      }
      if (last.type === 'event' && last.event.kind === ev.kind && last.event.summary === ev.summary) {
        out[out.length - 1] = { type: 'group', kind: ev.kind, events: [last.event, ev] }
        continue
      }
    }
    out.push({ type: 'event', event: ev })
  }
  return out
}

/** The incident's window in epoch ms: opened until resolved (or last seen while open). */
export function incidentWindow(i: Incident): { from: number; to: number } {
  const from = Date.parse(i.opened_at)
  const to = Date.parse(i.resolved_at ?? i.last_seen_at)
  return { from, to: Math.max(to, from + 60_000) }
}
