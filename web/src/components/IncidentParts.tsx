import { Fragment, useMemo, useState, type ReactNode } from 'react'
import { Link } from 'react-router'
import { ArrowRight, ChevronDown, ChevronRight } from 'lucide-react'
import { useEventSnapshotReport, useMetrics, useQueryDetail } from '../api/hooks'
import type { Finding, Incident, IncidentEvent, QueryRef, SessionBrief, WorkloadEntry } from '../api/types'
import { COMMAND_LABEL, commandColor } from '../lib/commands'
import { bytes, compact, ms, num, pct, seconds, time } from '../lib/format'
import { evidence, evidenceNumber, groupTimeline, incidentWindow, queryKey, ruleInfo } from '../lib/incident'
import { classInfo } from '../lib/load'
import { absolute } from '../lib/scope'
import { bucketSeries } from '../lib/statements'
import { BlockingTree } from './BlockingTree'
import { FindingCard } from './FindingCard'
import { seriesFrom, slot, type MetricDef } from './MetricCard'
import { CopyButton, formatSql, SqlText } from './SqlText'
import { TimeChart } from './TimeChart'
import { Badge, Card, cx, ErrorBox, Notice, selectCls, Spinner, Stat } from './ui'

const MIN = 60_000

/** "11.7 ms → 80.0 ms" with the change below it. */
function Change({ label, before, after, format, sub }: { label: string; before: number | null; after: number | null; format: (n: number | null) => string; sub?: ReactNode }) {
  return (
    <Stat
      label={label}
      value={
        <span className="inline-flex items-baseline gap-1.5">
          <span className="text-base font-medium text-muted">{format(before)}</span>
          <ArrowRight aria-label="to" className="h-4 w-4 self-center text-muted" />
          <span>{format(after)}</span>
        </span>
      }
      sub={sub}
    />
  )
}

/** Key numbers of the trigger finding, chosen per rule. */
function KeyNumbers({ f, share }: { f: Finding; share: number | null }) {
  const n = (label: string) => evidenceNumber(f, label)
  const s = (label: string) => {
    const v = evidence(f, label)
    return v === undefined || v === null ? '—' : String(v)
  }
  switch (f.rule_id) {
    case 'queries.mean_regression': {
      const m = f.metrics ?? {}
      const usual = evidence(f, 'compared with') === 'baseline'
      const before = usual ? (m.baseline_mean_ms ?? null) : n('mean time (previous window)')
      const after = n('mean time (current window)')
      const win = n('window length')
      return (
        <>
          <Change label="Time per call" before={before} after={after} format={ms} sub={before && after ? `${num(after / before, 1)}× slower than ${usual ? 'usual' : 'the previous window'}` : undefined} />
          {usual ? (
            <Change label="Calls per second" before={m.baseline_calls_per_s ?? null} after={m.calls_per_s ?? null} format={(v) => (v === null ? '—' : `${num(v, 2)}/s`)} sub="usual → now" />
          ) : (
            <Change label={`Calls per ${win ? seconds(win) : 'window'}`} before={n('calls (previous window)')} after={n('calls (current window)')} format={compact} />
          )}
          <Change label="Execution time per second" before={m.baseline_load_ms_per_s ?? m.previous_load_ms_per_s ?? null} after={m.load_ms_per_s ?? null} format={ms} sub="server time spent on this statement" />
          <Stat label="Share of database time" value={pct(m.share_of_load ?? share, 0)} sub="of all statements, last window" />
        </>
      )
    }
    case 'queries.long_running':
      return (
        <>
          <Stat label="Running for" value={seconds(n('running for'))} sub={`pid ${s('pid')}`} tone="warn" />
          <Stat label="Waiting on" value={s('wait event')} />
          {n('usual time per call') !== null && <Stat label="Usual time per call" value={ms(n('usual time per call'))} sub="baseline median" />}
          {evidence(f, 'blocked by') !== undefined && <Stat label="Blocked by" value={`pid ${s('blocked by')}`} tone="crit" />}
          {n('sessions waiting behind it') !== null && <Stat label="Sessions waiting behind it" value={num(n('sessions waiting behind it'))} tone="warn" />}
        </>
      )
    case 'blocking.long_chain':
    case 'blocking.idle_in_tx_blocker':
      return (
        <>
          <Stat label="Sessions waiting" value={num(n('transitively blocked (unique)'))} sub="blocked directly or behind others" tone="warn" />
          <Stat label="Root blocker" value={`pid ${s('root pid')}`} sub={s('root state') !== '—' ? s('root state') : s('state')} />
          {n('longest lock wait in chain') !== null && <Stat label="Longest lock wait" value={seconds(n('longest lock wait in chain'))} />}
          {n('observed duration') !== null && <Stat label="Blocking observed for" value={seconds(n('observed duration'))} />}
          <Stat label="Blocker's transaction age" value={seconds(n('root transaction age') ?? n('transaction age'))} />
        </>
      )
    case 'connections.pressure':
      return (
        <>
          <Stat label="Used connections" value={`${num(n('used connections'))} / ${num(n('max_connections'))}`} sub={`${num(n('reserved slots'))} reserved`} tone="warn" />
          <Stat label="Usage" value={pct(n('usage ratio'), 0)} />
          <Stat label="Active" value={num(n('active'))} sub="running a query" />
          <Stat label="Idle in transaction" value={num(n('idle in transaction'))} sub="holding a transaction open" />
        </>
      )
    case 'replication.slot_retained_wal':
      return (
        <>
          <Stat label="Slot" value={s('slot')} sub={s('type')} />
          <Stat label="Retained WAL" value={bytes(n('retained WAL'))} tone="warn" />
          <Stat label="Consumer connected" value={s('active') === 'true' ? 'yes' : 'no'} />
          <Stat label="WAL status" value={s('wal_status')} />
        </>
      )
  }
  return null
}

/** The first answer on the page: what happened, how big it is, and what the rule means. */
export function IncidentBrief({ incident: i, trigger, share }: { incident: Incident; trigger?: Finding; share: number | null }) {
  const info = ruleInfo(i.rule_id)
  const { from, to } = incidentWindow(i)
  return (
    <Card title="What happened" subtitle={info.name}>
      <div className="space-y-4">
        <p className="text-sm text-ink">
          {i.state === 'open' ? (
            <>
              Open since <strong className="font-semibold">{time(i.opened_at, { date: true })}</strong> ({seconds((to - from) / 1000)} so far) on <strong className="font-semibold">{i.target_name}</strong>.
            </>
          ) : (
            <>
              Lasted <strong className="font-semibold">{seconds((to - from) / 1000)}</strong> on <strong className="font-semibold">{i.target_name}</strong>, from {time(i.opened_at, { date: true })} to {time(i.resolved_at)} ({i.resolution}).
            </>
          )}
        </p>
        {trigger && trigger.observed.length > 0 && (
          <ul className="list-disc space-y-1 pl-5 text-sm text-ink">
            {trigger.observed.map((o) => (
              <li key={o}>{o}</li>
            ))}
          </ul>
        )}
        {trigger && (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
            <KeyNumbers f={trigger} share={share} />
          </div>
        )}
        {info.what && <p className="text-sm text-ink-2">{info.what}</p>}
      </div>
    </Card>
  )
}

/** Average time per call and calls per second of one statement around the incident. */
function StatementTrend({ targetId, identityId, from, to, highlight }: { targetId: number; identityId: number; from: number; to: number; highlight: { from: number; to: number } }) {
  const q = useQueryDetail(targetId, identityId, absolute(from, to))
  const s = useMemo(() => (q.data ? bucketSeries(q.data) : null), [q.data])
  if (q.error) return <ErrorBox error={q.error} title="Could not load the statement's history" />
  if (!q.data || !s) return <Spinner label="Loading the statement's history" />
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <TimeChart label="Average time per call" series={[{ key: 'mean', label: 'Average time per call', color: slot(0), points: s.mean }]} format={(v) => ms(v)} from={from} to={to} height={170} zoomable={false} highlight={highlight} />
      <TimeChart label="Calls per second" series={[{ key: 'calls', label: 'Calls per second', color: slot(1), points: s.calls }]} format={(v) => `${compact(v)}/s`} from={from} to={to} height={170} zoomable={false} highlight={highlight} />
    </div>
  )
}

/** The statement a statement rule fired for: its SQL and how it behaved around the incident. */
export function StatementCard({ incident: i, trigger, statement: stmt }: { incident: Incident; trigger: Finding; statement?: QueryRef }) {
  const { from, to } = incidentWindow(i)
  const win = (evidenceNumber(trigger, 'window length') ?? 900) * 1000
  const chartFrom = from - Math.max(30 * MIN, 2 * win + 5 * MIN)
  const chartTo = to + 10 * MIN
  const queryid = evidence(trigger, 'queryid')
  const db = evidence(trigger, 'database')
  const user = evidence(trigger, 'user')
  return (
    <Card
      title="The statement"
      subtitle={
        <span className="inline-flex flex-wrap items-center gap-x-2">
          {stmt && (
            <span className="inline-flex items-center gap-1">
              <span aria-hidden className="h-2.5 w-2.5 rounded-sm" style={{ background: commandColor(stmt.command) }} />
              {COMMAND_LABEL[stmt.command] ?? stmt.command}
            </span>
          )}
          <span>
            database {String(db ?? '—')} · user {String(user ?? '—')} · queryid {String(queryid ?? '—')}
          </span>
        </span>
      }
      actions={
        <>
          {stmt?.query_text && <CopyButton text={stmt.query_text} label="Copy SQL" />}
          {stmt && i.target_id && (
            <Link to={`/queries/${i.target_id}/${stmt.identity_id}`} className="inline-flex items-center gap-1 rounded-lg border border-line-strong px-2.5 py-1 text-xs font-medium hover:bg-surface-2">
              Open statement <ArrowRight aria-hidden className="h-3.5 w-3.5" />
            </Link>
          )}
        </>
      }
    >
      <div className="space-y-4">
        {stmt?.query_text ? (
          <div className="max-h-72 overflow-auto rounded-lg bg-surface-2 p-3">
            <SqlText sql={formatSql(stmt.query_text)} />
          </div>
        ) : stmt ? (
          <Notice>SQL text is not collected for this instance, so only the queryid is known. Turn on SQL text collection in Settings → Instances to see statements here.</Notice>
        ) : (
          <Notice>This statement is no longer in the monitoring store (it may have expired), so its SQL and history cannot be shown.</Notice>
        )}
        {stmt && i.target_id && (
          <>
            <StatementTrend targetId={i.target_id} identityId={stmt.identity_id} from={chartFrom} to={chartTo} highlight={{ from, to }} />
            <p className="text-xs text-muted">The shaded area is the incident. The rule compares the {seconds(win / 1000)} before it opened with the {seconds(win / 1000)} before that, so the slowdown starts before the shading.</p>
          </>
        )}
      </div>
    </Card>
  )
}

/** Top statements by execution time in one workload summary, compared with the window before. */
export function WorkloadCard({ events, refs, targetId, triggerKey }: { events: IncidentEvent[]; refs: Map<string, QueryRef>; targetId: number | null; triggerKey: string | null }) {
  const [pick, setPick] = useState(0)
  const ev = events[Math.min(pick, events.length - 1)]
  if (!ev) return null
  const p = ev.payload as { window_seconds?: number; current?: WorkloadEntry[]; previous?: WorkloadEntry[] }
  const prev = new Map((p.previous ?? []).map((q) => [queryKey(q.key), q]))
  const rows = p.current ?? []
  return (
    <Card
      title="What the database spent its time on"
      subtitle={`Statements with the most execution time in the ${seconds(p.window_seconds ?? 0)} before ${time(ev.at)} (pg_stat_statements). A statement that dominates here is what keeps the server busy.`}
      actions={
        events.length > 1 ? (
          <label className="flex items-center gap-1.5 text-xs text-muted">
            At
            <select className={selectCls} value={pick} onChange={(e) => setPick(Number(e.target.value))}>
              {events.map((e, n) => (
                <option key={e.id} value={n}>
                  {time(e.at)}
                  {n === 0 ? ' (trigger)' : ''}
                </option>
              ))}
            </select>
          </label>
        ) : undefined
      }
    >
      {rows.length === 0 ? (
        <p className="text-sm text-muted">No statement activity was recorded in this window.</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[640px] text-sm">
            <caption className="sr-only">Top statements by total execution time</caption>
            <thead>
              <tr className="border-b border-line text-left text-xs text-muted">
                <th className="py-2 pr-3 font-medium">Statement</th>
                <th className="py-2 pr-3 text-right font-medium">Calls</th>
                <th className="whitespace-nowrap py-2 pr-3 text-right font-medium">Per call</th>
                <th className="py-2 pr-3 text-right font-medium">Before</th>
                <th className="whitespace-nowrap py-2 pr-3 text-right font-medium">Total time</th>
                <th className="w-32 py-2 font-medium">Share</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((q) => {
                const k = queryKey(q.key)
                const ref = refs.get(k)
                const before = prev.get(k)
                const text = q.text ?? ref?.query_text ?? null
                const slower = before?.mean_exec_ms && q.mean_exec_ms ? q.mean_exec_ms / before.mean_exec_ms : null
                const label = text ? <SqlText sql={text} clamp /> : <span className="font-mono text-xs text-ink-2">queryid {q.key.queryid}</span>
                return (
                  <tr key={k} className="border-b border-line align-top last:border-0">
                    <td className={cx('max-w-[28rem] py-2 pr-3', k === triggerKey && 'shadow-[inset_3px_0_0_var(--crit-mark)] pl-2')}>
                      {ref && targetId ? (
                        <Link to={`/queries/${targetId}/${ref.identity_id}`} className="block hover:underline">
                          {label}
                        </Link>
                      ) : (
                        label
                      )}
                      <span className="mt-0.5 flex flex-wrap items-center gap-1.5 text-xs text-muted">
                        {q.datname} · {q.username}
                        {k === triggerKey && <Badge tone="crit">this incident</Badge>}
                      </span>
                    </td>
                    <td className="num whitespace-nowrap py-2 pr-3 text-right">{compact(q.calls)}</td>
                    <td className="num whitespace-nowrap py-2 pr-3 text-right">{ms(q.mean_exec_ms)}</td>
                    <td className="num whitespace-nowrap py-2 pr-3 text-right text-muted">
                      {before ? ms(before.mean_exec_ms) : 'new'}
                      {slower !== null && slower >= 1.5 && <span className="ml-1 font-medium text-warn">{slower >= 100 ? '>100' : num(slower, 1)}×</span>}
                    </td>
                    <td className="num whitespace-nowrap py-2 pr-3 text-right">{ms(q.total_exec_ms)}</td>
                    <td className="py-2">
                      <span className="flex items-center gap-2">
                        <span className="h-1.5 flex-1 overflow-hidden rounded-full bg-surface-3">
                          <span className="block h-full rounded-full bg-accent-fill" style={{ width: `${Math.round((q.share_of_total_time ?? 0) * 100)}%` }} />
                        </span>
                        <span className="num w-9 text-right text-xs text-ink-2">{pct(q.share_of_total_time, 0)}</span>
                      </span>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  )
}

function stateTone(s: SessionBrief): 'crit' | 'warn' | 'accent' | 'muted' {
  if (s.wait_event_type === 'Lock') return 'warn'
  if (s.state?.startsWith('idle in transaction')) return 'warn'
  if (s.state === 'active') return 'accent'
  return 'muted'
}

/** Who was connected and doing what when the incident opened, with their SQL. */
export function SessionsCard({ event, rootPid }: { event: IncidentEvent; rootPid: number | null }) {
  const p = event.payload as { counts?: { client: number; active: number; idle_in_transaction: number; waiting_lock: number; listed: number }; sessions?: SessionBrief[]; query_text_collected?: boolean }
  const [open, setOpen] = useState<number | null>(null)
  const list = p.sessions ?? []
  const c = p.counts
  return (
    <Card
      title="Sessions when it started"
      subtitle={
        c
          ? `${c.active} active, ${c.idle_in_transaction} idle in transaction, ${c.waiting_lock} waiting for a lock, of ${c.client} client sessions at ${time(event.at)}. Blockers first, then the longest running.`
          : undefined
      }
    >
      {list.length === 0 ? (
        <p className="text-sm text-muted">No session was running a query or holding a transaction at that moment.</p>
      ) : (
        <div className="overflow-x-auto">
          {!p.query_text_collected && <div className="mb-2"><Notice>SQL text is not collected for this instance, so sessions are shown without their statements.</Notice></div>}
          <table className="w-full min-w-[640px] text-sm">
            <caption className="sr-only">Client sessions at the trigger</caption>
            <thead>
              <tr className="border-b border-line text-left text-xs text-muted">
                <th className="py-2 pr-3 font-medium">Session</th>
                <th className="py-2 pr-3 font-medium">State</th>
                <th className="py-2 pr-3 text-right font-medium">Running for</th>
                <th className="py-2 font-medium">Statement</th>
              </tr>
            </thead>
            <tbody>
              {list.map((s) => {
                const idleTx = s.state?.startsWith('idle in transaction')
                const isOpen = open === s.pid
                return (
                  <Fragment key={s.pid}>
                    <tr className="border-b border-line align-top">
                      <td className={cx('py-2 pr-3', s.pid === rootPid && 'shadow-[inset_3px_0_0_var(--crit-mark)] pl-2')}>
                        <span className="font-mono font-semibold">pid {s.pid}</span>
                        <span className="block text-xs text-muted">
                          {s.user ?? '?'} · {s.database ?? '?'}
                          {s.application_name ? ` · ${s.application_name}` : ''}
                        </span>
                        <span className="mt-0.5 flex flex-wrap gap-1">
                          {s.pid === rootPid && <Badge tone="crit">root blocker</Badge>}
                          {!!s.blocks && s.pid !== rootPid && <Badge tone="crit">blocks {s.blocks}</Badge>}
                          {!!s.blocked_by?.length && <Badge tone="warn">waits for pid {s.blocked_by.join(', ')}</Badge>}
                        </span>
                      </td>
                      <td className="py-2 pr-3">
                        <Badge tone={stateTone(s)}>{s.state ?? 'unknown'}</Badge>
                        {s.wait_event_type && (
                          <span className="mt-0.5 block text-xs text-muted">
                            waiting: {s.wait_event_type}
                            {s.wait_event ? ` / ${s.wait_event}` : ''}
                          </span>
                        )}
                      </td>
                      <td className="num py-2 pr-3 text-right">
                        {idleTx ? seconds(s.transaction_age_seconds) : seconds(s.query_age_seconds)}
                        <span className="block text-xs text-muted">{idleTx ? 'transaction open' : s.transaction_age_seconds !== null ? `tx ${seconds(s.transaction_age_seconds)}` : ''}</span>
                      </td>
                      <td className="max-w-[30rem] py-2">
                        {s.query ? (
                          <button type="button" onClick={() => setOpen(isOpen ? null : s.pid)} aria-expanded={isOpen} className="flex w-full min-w-0 items-start gap-1 text-left">
                            {isOpen ? <ChevronDown aria-hidden className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted" /> : <ChevronRight aria-hidden className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted" />}
                            <span className="min-w-0 flex-1">
                              <SqlText sql={s.query} clamp />
                              {idleTx && <span className="text-xs text-muted">last statement before going idle</span>}
                            </span>
                          </button>
                        ) : (
                          <span className="text-xs text-muted">—</span>
                        )}
                      </td>
                    </tr>
                    {isOpen && s.query && (
                      <tr className="border-b border-line">
                        <td colSpan={4} className="pb-3">
                          <div className="flex items-start gap-2 rounded-lg bg-surface-2 p-3">
                            <div className="min-w-0 flex-1">
                              <SqlText sql={formatSql(s.query)} />
                              {s.query_truncated && <p className="mt-1 text-xs text-muted">Truncated by the collector.</p>}
                            </div>
                            <CopyButton text={s.query} />
                          </div>
                        </td>
                      </tr>
                    )}
                  </Fragment>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  )
}

/** The blocking graph stored with the incident (latest first, selectable). */
export function BlockingCard({ events }: { events: IncidentEvent[] }) {
  const [pick, setPick] = useState(events.length - 1)
  const ev = events[Math.max(0, Math.min(pick, events.length - 1))]
  const q = useEventSnapshotReport(ev?.id, ev?.payload.snapshot)
  if (!ev) return null
  return (
    <Card
      title="Who blocks whom"
      subtitle={`${ev.summary} Recorded at ${time(ev.at)}; each root blocker holds the locks the sessions below it wait for.`}
      actions={
        events.length > 1 ? (
          <label className="flex items-center gap-1.5 text-xs text-muted">
            Graph at
            <select className={selectCls} value={pick} onChange={(e) => setPick(Number(e.target.value))}>
              {events.map((e, n) => (
                <option key={e.id} value={n}>
                  {time(e.at)}
                </option>
              ))}
            </select>
          </label>
        ) : undefined
      }
    >
      {q.data ? <BlockingTree report={q.data} /> : q.error ? <ErrorBox error={q.error} title="Could not analyze the stored graph" /> : <Spinner label="Analyzing the blocking graph" />}
    </Card>
  )
}

const CONTEXT: { title: string; defs: MetricDef[]; format: (v: number) => string; integer?: boolean }[] = [
  {
    title: 'Sessions',
    defs: [
      { metric: 'sessions.active', label: 'Active', color: slot(0) },
      { metric: 'sessions.idle_in_transaction', label: 'Idle in transaction', color: slot(2) },
      { metric: 'sessions.waiting_lock', label: 'Waiting for a lock', color: slot(3) },
    ],
    format: (v) => num(v, 1),
  },
  { title: 'Query execution time', defs: [{ metric: 'statements.exec_ms.per_s', label: 'Execution time', color: slot(0) }], format: (v) => `${ms(v)}/s` },
  { title: 'Blocked sessions', defs: [{ metric: 'blocking.blocked_sessions', label: 'Blocked', color: slot(1) }], format: (v) => num(v), integer: true },
  { title: 'Used connections', defs: [{ metric: 'connections.used', label: 'Used', color: slot(6) }], format: (v) => num(v), integer: true },
]

/** The instance's main signals from before the incident until after it, with the incident shaded. */
export function ContextCharts({ incident: i }: { incident: Incident }) {
  const { from, to } = incidentWindow(i)
  const chartFrom = from - 30 * MIN
  const chartTo = to + 10 * MIN
  const names = useMemo(() => CONTEXT.flatMap((c) => c.defs.map((d) => d.metric)), [])
  const q = useMetrics(i.target_id ?? 0, names, absolute(chartFrom, chartTo))
  const data = q.data
  return (
    <Card title="The database around the incident" subtitle="30 minutes before it opened until 10 minutes after it ended; the incident is shaded.">
      {q.error ? (
        <ErrorBox error={q.error} title="Could not load the charts" />
      ) : !data ? (
        <Spinner label="Loading charts" />
      ) : (
        <div className="grid gap-5 lg:grid-cols-2">
          {CONTEXT.map((c) => (
            <div key={c.title}>
              <h3 className="mb-1 text-sm font-medium text-ink">{c.title}</h3>
              <TimeChart
                label={c.title}
                series={seriesFrom(data, c.defs)}
                format={c.format}
                integer={c.integer}
                from={chartFrom}
                to={chartTo}
                height={160}
                zoomable={false}
                highlight={{ from, to }}
                gaps={data.gaps.map((g) => ({ from: Date.parse(g.from), to: Date.parse(g.to) }))}
              />
            </div>
          ))}
        </div>
      )}
    </Card>
  )
}

const KIND_LABEL: Record<string, string> = {
  pre_trigger: 'before',
  sample: 'during',
  post_trigger: 'clearing',
  trigger: 'opened',
  retriggered: 'returned',
  resolved: 'resolved',
  workload: 'workload',
  sessions: 'sessions',
  blocking_snapshot: 'blocking graph',
  finding: 'also observed',
  gap: 'gap',
  truncated: 'truncated',
  recorder_restarted: 'restart',
  visibility: 'visibility',
}

const KIND_TONE: Record<string, 'crit' | 'warn' | 'good' | 'accent' | 'muted'> = {
  trigger: 'crit',
  retriggered: 'crit',
  resolved: 'good',
  gap: 'warn',
  truncated: 'warn',
  recorder_restarted: 'warn',
  visibility: 'warn',
  blocking_snapshot: 'accent',
  sessions: 'accent',
  finding: 'accent',
}

function KindBadge({ kind }: { kind: string }) {
  return <Badge tone={KIND_TONE[kind] ?? 'muted'}>{KIND_LABEL[kind] ?? kind.replace(/_/g, ' ')}</Badge>
}

/** The recorded evidence in order; identical routine samples are collapsed. */
export function Timeline({ events }: { events: IncidentEvent[] }) {
  const rows = useMemo(() => groupTimeline(events), [events])
  const [expanded, setExpanded] = useState<Set<number>>(() => new Set())
  const toggle = (id: number) =>
    setExpanded((s) => {
      const n = new Set(s)
      if (n.has(id)) n.delete(id)
      else n.add(id)
      return n
    })
  const line = (ev: IncidentEvent) => (
    <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1 text-sm">
      <span className="num w-[4.5rem] shrink-0 text-xs text-muted">{time(ev.at)}</span>
      <KindBadge kind={ev.kind} />
      <span className="min-w-0 flex-1">{ev.summary}</span>
    </div>
  )
  return (
    <ol className="space-y-2">
      {rows.map((r) => {
        if (r.type === 'event') {
          const ev = r.event
          return (
            <li key={ev.id} className={cx('border-l-2 pl-3', ev.kind === 'trigger' || ev.kind === 'resolved' ? 'border-accent' : 'border-line-strong')}>
              {line(ev)}
              {ev.kind === 'finding' && !!ev.payload.finding && (
                <div className="mt-1.5">
                  <FindingCard finding={ev.payload.finding as Finding} />
                </div>
              )}
            </li>
          )
        }
        const first = r.events[0]
        const last = r.events[r.events.length - 1]
        if (!first || !last) return null
        const open = expanded.has(first.id)
        return (
          <li key={first.id} className="border-l-2 border-line pl-3">
            <button type="button" onClick={() => toggle(first.id)} aria-expanded={open} className="flex w-full flex-wrap items-baseline gap-x-2 gap-y-1 text-left text-sm text-ink-2 hover:text-ink">
              <span className="num w-[4.5rem] shrink-0 text-xs text-muted">{time(first.at)}</span>
              <KindBadge kind={r.kind} />
              <span className="min-w-0 flex-1">
                {r.events.length} unchanged {r.kind === 'workload' ? 'summaries' : 'samples'} until {time(last.at)}: {first.summary}
              </span>
              {open ? <ChevronDown aria-hidden className="h-4 w-4 self-center text-muted" /> : <ChevronRight aria-hidden className="h-4 w-4 self-center text-muted" />}
            </button>
            {open && (
              <ol className="mt-1.5 space-y-1 opacity-80">
                {r.events.map((ev) => (
                  <li key={ev.id}>{line(ev)}</li>
                ))}
              </ol>
            )}
          </li>
        )
      })}
    </ol>
  )
}


interface LoadSummary {
  window_seconds: number
  ticks: number
  active_aas: number
  classes: Record<string, number>
  waits: { wait_class: string; wait_event: string; aas: number }[]
  statements: { dbid: number; userid: number; queryid: string; datname: string; username: string; aas: number; classes: Record<string, number> }[]
}

/**
 * Database load in the minutes before the trigger, stored with the incident:
 * how busy the server was, on what it waited, and which statements were
 * active most. It stays when the sampled history expires.
 */
export function LoadSummaryCard({ event, refs, targetId }: { event: IncidentEvent; refs: Map<string, QueryRef>; targetId: number | null }) {
  const p = event.payload as unknown as LoadSummary
  const classes = Object.entries(p.classes ?? {}).sort((a, b) => b[1] - a[1])
  const total = classes.reduce((n, [, v]) => n + v, 0)
  const byActivity = new Map([...refs.values()].map((r) => [`${r.key.dbid}/${r.key.userid}/${r.key.queryid}`, r]))
  return (
    <Card title="Load before it started" subtitle={`Active session sampling in the ${seconds(p.window_seconds)} before ${time(event.at)}: ${num(p.active_aas, 1)} sessions were active on average (${num(p.ticks)} samples).`}>
      <div className="space-y-4">
        <div>
          <div className="flex h-3 w-full overflow-hidden rounded-full bg-surface-3" role="img" aria-label={classes.map(([k, v]) => `${classInfo(k).label} ${num(v, 2)}`).join(', ')}>
            {classes.map(([k, v]) => (
              <span key={k} className="h-full" style={{ width: `${(v / (total || 1)) * 100}%`, background: classInfo(k).color }} />
            ))}
          </div>
          <ul className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-ink-2">
            {classes.map(([k, v]) => (
              <li key={k} className="flex items-center gap-1.5" title={classInfo(k).hint}>
                <span aria-hidden className="h-2.5 w-2.5 rounded-sm" style={{ background: classInfo(k).color }} />
                {classInfo(k).label} <span className="num font-medium text-ink">{num(v, 2)}</span>
              </li>
            ))}
          </ul>
        </div>
        {(p.statements ?? []).length > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[560px] text-sm">
              <caption className="sr-only">Statements by database load before the trigger</caption>
              <thead>
                <tr className="border-b border-line text-left text-xs text-muted">
                  <th className="py-2 pr-3 font-medium">Statement</th>
                  <th className="whitespace-nowrap py-2 pr-3 text-right font-medium">Avg active</th>
                  <th className="py-2 font-medium">Mostly</th>
                </tr>
              </thead>
              <tbody>
                {p.statements.map((st) => {
                  const ref = byActivity.get(`${st.dbid}/${st.userid}/${st.queryid}`)
                  const main = Object.entries(st.classes ?? {}).sort((a, b) => b[1] - a[1])[0]
                  const body = ref?.query_text ? <SqlText sql={ref.query_text} clamp /> : <span className="font-mono text-xs text-ink-2">{st.queryid === '0' ? 'Statements without a query ID' : `queryid ${st.queryid}`}</span>
                  return (
                    <tr key={`${st.dbid}/${st.userid}/${st.queryid}`} className="border-b border-line align-top last:border-0">
                      <td className="max-w-[30rem] py-2 pr-3">
                        {ref && targetId ? (
                          <Link to={`/queries/${targetId}/${ref.identity_id}`} className="block hover:underline">
                            {body}
                          </Link>
                        ) : (
                          body
                        )}
                        <span className="mt-0.5 block text-xs text-muted">
                          {st.datname || '?'} · {st.username || '?'}
                        </span>
                      </td>
                      <td className="num whitespace-nowrap py-2 pr-3 text-right font-medium">{num(st.aas, 2)}</td>
                      <td className="py-2">
                        {main && (
                          <span className="inline-flex items-center gap-1.5 text-xs text-ink-2">
                            <span aria-hidden className="h-2.5 w-2.5 rounded-sm" style={{ background: classInfo(main[0]).color }} />
                            {classInfo(main[0]).label} {pct(main[1] / (st.aas || 1), 0)}
                          </span>
                        )}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </Card>
  )
}
