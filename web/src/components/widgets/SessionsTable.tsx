import { useMemo, useState } from 'react'
import { Search } from 'lucide-react'
import type { FleetLiveTarget, Report, SnapshotSession } from '../../api/types'
import { ageOf, seconds, time } from '../../lib/format'
import { describeWait } from '../BlockingTree'
import { CopyButton, formatSql, SqlText } from '../SqlText'
import { tableCls, tdCls, tdNum, Th, trCls } from '../Table'
import { Badge, Drawer, Empty, inputCls, KeyValue, Segmented } from '../ui'

export type SessionFilter = 'all' | 'active' | 'idle_tx' | 'waiting'

export function stateTone(s: string | null): 'good' | 'warn' | 'crit' | 'muted' {
  if (!s) return 'muted'
  if (s === 'active') return 'good'
  if (s.startsWith('idle in transaction')) return 'warn'
  return 'muted'
}

export interface SessionRow {
  key: string
  target: FleetLiveTarget
  report: Report
  s: SnapshotSession
  /** How long the current query (active) or the current state has lasted. */
  duration: number | null
  txAge: number | null
  blocked: boolean
}

/** Client sessions of every target in a fleet live response, longest first. */
export function sessionRows(targets: FleetLiveTarget[]): SessionRow[] {
  const out: SessionRow[] = []
  for (const t of targets) {
    const r = t.report
    if (!r) continue
    const now = r.snapshot.server_time
    const blocked = new Set(r.analysis.nodes.filter((n) => n.blocked_by.length).map((n) => n.id))
    for (const s of r.snapshot.sessions) {
      if (s.backend_type !== 'client backend' && s.backend_type !== null) continue
      out.push({
        key: `${t.target.id}:${s.pid}`,
        target: t,
        report: r,
        s,
        duration: ageOf(s.state === 'active' ? s.query_start : s.state_change, now),
        txAge: ageOf(s.xact_start, now),
        blocked: blocked.has(s.pid),
      })
    }
  }
  return out.sort((a, b) => (b.duration ?? -1) - (a.duration ?? -1))
}

export function SessionDetail({ s, report, instance }: { s: SnapshotSession; report: Report; instance?: string }) {
  const now = report.snapshot.server_time
  const node = report.analysis.nodes.find((n) => n.id === s.pid)
  return (
    <div className="space-y-4">
      <KeyValue
        items={[
          ...(instance ? [['Instance', instance] as [string, string]] : []),
          ['State', <Badge key="s" tone={stateTone(s.state)}>{s.state ?? 'unknown'}</Badge>],
          ['User', s.user ?? '—'],
          ['Database', s.database ?? '—'],
          ['Application', s.application_name || '—'],
          ['Client', s.client_addr ?? 'local socket'],
          ['Backend', s.backend_type ?? '—'],
          ['Connected', `${time(s.backend_start, { date: true })} (${seconds(ageOf(s.backend_start, now))} ago)`],
          ['Transaction age', s.xact_start ? seconds(ageOf(s.xact_start, now)) : 'no open transaction'],
          ['Query running for', s.query_start && s.state === 'active' ? seconds(ageOf(s.query_start, now)) : '—'],
          ['Wait event', s.wait_event ? `${s.wait_event_type}: ${s.wait_event}` : 'none'],
          ['Waiting for', describeWait(s) ?? 'nothing'],
          ['Blocked by', node?.blocked_by.length ? node.blocked_by.join(', ') : 'nobody'],
          ['Blocks', node?.blocks.length ? `${node.blocks.join(', ')} (${node.transitive_blocked_count} in total)` : 'nobody'],
        ]}
      />
      <div>
        <div className="mb-1 flex items-center justify-between">
          <h3 className="text-sm font-semibold">{s.state === 'active' ? 'Current query' : 'Last query'}</h3>
          {s.query && <CopyButton text={s.query} />}
        </div>
        {s.query ? (
          <div className="rounded-lg bg-surface-2 p-3">
            <SqlText sql={formatSql(s.query)} />
          </div>
        ) : (
          <p className="text-sm text-muted">{s.details_visible ? 'Query text is not collected for this instance.' : 'Hidden: the monitoring role cannot see this session (grant pg_read_all_stats).'}</p>
        )}
        {s.query_truncated && <p className="mt-1 text-xs text-muted">Truncated by track_activity_query_size.</p>}
      </div>
    </div>
  )
}

function StateCell({ r }: { r: SessionRow }) {
  return <Badge tone={r.blocked ? 'crit' : stateTone(r.s.state)}>{r.blocked ? 'blocked' : (r.s.state ?? 'hidden')}</Badge>
}

/**
 * Sessions across instances. `compact` is the Overview card (busy sessions
 * only, no filters); the full table adds filters, search, and the query.
 */
export function SessionsTable({ targets, compact = false, limit }: { targets: FleetLiveTarget[]; compact?: boolean; limit?: number }) {
  const [filter, setFilter] = useState<SessionFilter>('all')
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState<string | null>(null)
  const all = useMemo(() => sessionRows(targets), [targets])
  const multi = targets.length > 1

  const rows = useMemo(() => {
    const term = search.trim().toLowerCase()
    return all
      .filter((r) => (compact ? r.s.state !== 'idle' : true))
      .filter(({ s, blocked }) => (filter === 'active' ? s.state === 'active' : filter === 'idle_tx' ? s.state?.startsWith('idle in transaction') : filter === 'waiting' ? blocked || s.wait_event_type === 'Lock' : true))
      .filter(({ s, target }) => !term || [s.query, s.user, s.application_name, s.database, String(s.pid), s.client_addr, target.target.name].some((x) => x?.toLowerCase().includes(term)))
  }, [all, filter, search, compact])
  const shown = limit ? rows.slice(0, limit) : rows
  const sel = all.find((r) => r.key === selected)

  return (
    <>
      {!compact && (
        <div className="flex flex-wrap items-center gap-2 border-b border-line px-5 pb-3">
          <Segmented label="Session state" value={filter} onChange={setFilter} items={[{ value: 'all', label: 'All' }, { value: 'active', label: 'Running' }, { value: 'idle_tx', label: 'Idle in transaction' }, { value: 'waiting', label: 'Waiting / blocked' }]} />
          <div className="relative ml-auto">
            <Search aria-hidden className="pointer-events-none absolute top-2 left-2.5 h-4 w-4 text-muted" />
            <input aria-label="Search sessions" placeholder="Search query, user, app, pid…" className={`${inputCls} w-64 pl-8`} value={search} onChange={(e) => setSearch(e.target.value)} />
          </div>
        </div>
      )}
      {shown.length === 0 ? (
        <div className={compact ? 'px-5 pb-5' : 'p-5'}>
          <Empty title={compact ? 'No busy sessions' : 'No sessions match'}>{compact || (filter === 'all' && !search) ? 'No client session is running a query or holding a transaction right now.' : 'Try another filter.'}</Empty>
        </div>
      ) : (
        <div className="overflow-x-auto">
          <table className={tableCls}>
            <caption className="sr-only">Sessions</caption>
            <thead>
              <tr>
                <Th className="pl-5">PID</Th>
                {multi && !compact && <Th>Instance</Th>}
                <Th>User</Th>
                {!compact && <Th>Database</Th>}
                <Th>State</Th>
                <Th align="right">Duration</Th>
                <Th className={compact ? 'pr-5' : undefined}>{compact ? 'Wait' : 'Wait event'}</Th>
                {!compact && <Th align="right">Transaction</Th>}
                {!compact && <Th className="w-[34%] pr-5">Query</Th>}
              </tr>
            </thead>
            <tbody>
              {shown.map((r) => (
                <tr key={r.key} className={`${trCls} cursor-pointer`} onClick={() => setSelected(r.key)}>
                  <td className={`${tdCls} pl-5`}>
                    <button type="button" className="font-mono text-accent hover:underline" onClick={() => setSelected(r.key)}>
                      {r.s.pid}
                    </button>
                    {compact && multi && <div className="text-[11px] whitespace-nowrap text-muted">{r.target.target.name}</div>}
                  </td>
                  {multi && !compact && <td className={`${tdCls} whitespace-nowrap text-xs`}>{r.target.target.name}</td>}
                  <td className={`${tdCls} whitespace-nowrap`}>
                    {r.s.user ?? '—'}
                    {compact && <div className="text-[11px] text-muted">{r.s.database ?? '—'}</div>}
                  </td>
                  {!compact && <td className={`${tdCls} whitespace-nowrap`}>{r.s.database ?? '—'}</td>}
                  <td className={tdCls}>
                    <StateCell r={r} />
                  </td>
                  <td className={tdNum}>{seconds(r.duration)}</td>
                  <td className={`${tdCls} text-xs text-ink-2 ${compact ? 'pr-5' : 'whitespace-nowrap'}`}>{r.s.wait_event ? (compact ? r.s.wait_event : `${r.s.wait_event_type}: ${r.s.wait_event}`) : '—'}</td>
                  {!compact && <td className={tdNum}>{r.txAge === null ? '—' : seconds(r.txAge)}</td>}
                  {!compact && <td className={`${tdCls} pr-5`}>{r.s.query ? <SqlText sql={r.s.query} clamp /> : <span className="text-xs text-muted">{r.s.details_visible ? '—' : 'hidden'}</span>}</td>}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <Drawer open={!!sel} title={sel ? `Session ${sel.s.pid}` : ''} onClose={() => setSelected(null)}>
        {sel && <SessionDetail s={sel.s} report={sel.report} instance={sel.target.target.name} />}
      </Drawer>
    </>
  )
}
