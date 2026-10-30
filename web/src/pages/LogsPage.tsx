import { useMemo, useState } from 'react'
import { Link } from 'react-router'
import { useLogEvents, useLogSummary } from '../api/hooks'
import type { LogEvent, LogKind, LogStatement, LogSummary } from '../api/types'
import { slot } from '../components/MetricCard'
import { useNumParam } from '../components/Shell'
import { SqlText } from '../components/SqlText'
import { tableCls, tdCls, tdNum, Th, trCls } from '../components/Table'
import { TimeChart, type ChartSeries } from '../components/TimeChart'
import { Badge, Card, Drawer, Empty, Load, Notice, Tabs } from '../components/ui'
import { ago, bytes, compact, ms, num, time } from '../lib/format'
import { rangeWindow, useRange } from '../lib/scope'

const KIND_LABEL: Record<LogKind, string> = {
  slow_query: 'Slow statements',
  plan: 'Plans (auto_explain)',
  lock_wait: 'Lock waits',
  deadlock: 'Deadlocks',
  autovacuum: 'Autovacuum',
  autoanalyze: 'Autoanalyze',
  temp_file: 'Temporary files',
  checkpoint: 'Checkpoints',
  error: 'Errors',
}
const KINDS = Object.keys(KIND_LABEL) as LogKind[]

/** Events per kind over time, one bar series per kind that occurred. */
function countSeries(d: LogSummary): ChartSeries[] {
  const step = d.step_seconds * 1000
  const byKind = new Map<LogKind, Map<number, number>>()
  for (const c of d.counts) {
    const m = byKind.get(c.kind) ?? new Map<number, number>()
    m.set(Date.parse(c.t), c.n)
    byKind.set(c.kind, m)
  }
  const from = Math.floor(Date.parse(d.from) / step) * step
  const to = Date.parse(d.to)
  return KINDS.filter((k) => byKind.has(k)).map((k, i) => {
    const m = byKind.get(k) ?? new Map<number, number>()
    const points: [number, number | null][] = []
    for (let t = from; t < to; t += step) points.push([t, m.get(t) ?? 0])
    return { key: k, label: KIND_LABEL[k], color: slot(i), points, bars: true }
  })
}

function Setup({ d, now }: { d: LogSummary; now: number }) {
  const missing = d.settings.filter((s) => !s.ok)
  return (
    <Card title="Log agent" subtitle="The log agent runs on the database host, follows the server log, and sends events here. It never connects to PostgreSQL; statements are normalised and messages redacted before they leave the host.">
      {d.agents.length === 0 ? (
        <div className="space-y-2 text-sm">
          <p className="text-ink-2">No log agent sends this instance's logs yet.</p>
          <ol className="list-decimal space-y-1 pl-5 text-ink-2">
            <li>
              Create a token for shipping logs of this instance in <Link to="/settings/tokens" className="text-accent hover:underline">Settings → API tokens</Link>.
            </li>
            <li>On the database host, run the agent with the token and the log directory:</li>
          </ol>
          <pre className="overflow-x-auto rounded-lg bg-surface-2 p-3 text-xs">{`LOCKLENS_AGENT_TOKEN=llk_... locklens agent \\
  --server ${window.location.origin} \\
  --log /var/lib/postgresql/data/log --state-dir /var/lib/locklens-agent`}</pre>
        </div>
      ) : (
        <ul className="space-y-1 text-sm">
          {d.agents.map((a) => (
            <li key={a.token_id} className="flex flex-wrap items-center gap-2">
              <Badge tone={now - Date.parse(a.last_seen_at) < 5 * 60_000 ? 'good' : 'warn'}>{a.hostname}</Badge>
              <span className="text-ink-2">
                token {a.token_name} · agent {a.version} · last contact {ago(a.last_seen_at)} · {compact(a.received)} events
              </span>
              {a.sources.length > 0 && <span className="font-mono text-xs text-muted">{a.sources.join(', ')}</span>}
            </li>
          ))}
        </ul>
      )}
      {d.settings.length > 0 && (
        <div className="mt-4">
          <h4 className="text-xs font-semibold text-ink">Logging settings of the instance {missing.length > 0 && <span className="font-normal text-muted">({missing.length} would add events; LockLens never changes settings)</span>}</h4>
          <ul className="mt-1 space-y-0.5 text-xs">
            {d.settings.map((s) => (
              <li key={s.name} className="flex gap-2">
                <Badge tone={s.ok ? 'good' : 'muted'}>{s.ok ? 'on' : 'off'}</Badge>
                <span className="font-mono">{s.name} = {s.value || "''"}</span>
                <span className="text-muted">{s.advice}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </Card>
  )
}

function threshold(d: LogSummary): string | null {
  const v = d.settings.find((s) => s.name === 'log_min_duration_statement')?.value
  return v && v !== '-1' ? v : null
}

function Statements({ d, onOpen }: { d: LogSummary; onOpen: (s: LogStatement) => void }) {
  const min = threshold(d)
  return (
    <Card
      pad={false}
      title="Slow statements from the log"
      subtitle={`Executions the server logged${min !== null ? ` (slower than log_min_duration_statement = ${min} ms)` : ''}. Percentiles are of those executions only, not of every execution.`}
    >
      {d.statements.length === 0 ? (
        <div className="p-4">
          <Empty title="No slow statements logged in this time range">Set log_min_duration_statement (or load auto_explain) to log them.</Empty>
        </div>
      ) : (
        <div className="overflow-x-auto">
          <table className={tableCls}>
            <thead>
              <tr>
                <Th>Statement</Th>
                <Th align="right">Logged</Th>
                <Th align="right">p50</Th>
                <Th align="right">p95</Th>
                <Th align="right">p99</Th>
                <Th align="right">Max</Th>
                <Th align="right">Total</Th>
                <Th>Plans</Th>
              </tr>
            </thead>
            <tbody>
              {d.statements.map((s) => (
                <tr key={s.fingerprint} className={`${trCls} cursor-pointer`} onClick={() => onOpen(s)}>
                  <td className={`${tdCls} max-w-0 min-w-72`}>
                    {s.query ? <SqlText sql={s.query} clamp /> : <span className="text-xs text-muted">no text</span>}
                    <span className="text-xs text-muted">{s.source === 'plan' ? 'from auto_explain' : 'from the slow statement log'} · last {ago(s.last_at)}</span>
                  </td>
                  <td className={tdNum}>{compact(s.count)}</td>
                  <td className={tdNum}>{ms(s.p50_ms)}</td>
                  <td className={tdNum}>{ms(s.p95_ms)}</td>
                  <td className={tdNum}>{ms(s.p99_ms)}</td>
                  <td className={tdNum}>{ms(s.max_ms)}</td>
                  <td className={tdNum}>{ms(s.total_ms)}</td>
                  <td className={tdCls}>{s.plans > 0 ? <Badge tone="accent">{num(s.plans)}</Badge> : <span className="text-xs text-muted">none</span>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  )
}

function PlanText({ plan }: { plan: unknown }) {
  const text = typeof plan === 'string' ? plan : JSON.stringify(plan, null, 2)
  return <pre className="max-h-96 overflow-auto rounded-lg bg-surface-2 p-3 text-xs">{text}</pre>
}

function StatementDrawer({ id, s, onClose }: { id: number; s: LogStatement | null; onClose: () => void }) {
  const [range] = useRange()
  const q = useLogEvents(id, ['plan'], range, s?.fingerprint ?? '')
  return (
    <Drawer open={!!s} title="Logged statement" onClose={onClose}>
      {s && (
        <div className="space-y-4 text-sm">
          {s.query && <SqlText sql={s.query} />}
          {s.query_id !== null && <p className="font-mono text-xs text-muted">queryid {s.query_id} (pg_stat_statements)</p>}
          <p className="text-xs text-muted">Constants were replaced by $1, $2, … on the database host; plan conditions show ? for values.</p>
          <Load q={q} label="Loading plans">
            {(d) =>
              d.events.length === 0 ? (
                <p className="text-xs text-muted">No auto_explain plan of this statement in the time range.</p>
              ) : (
                <div className="space-y-3">
                  {d.events.slice(0, 5).map((e) => (
                    <div key={e.id}>
                      <p className="text-xs text-muted">
                        {time(e.at, { date: true, seconds: true })} · {ms(e.duration_ms ?? null)}
                      </p>
                      <PlanText plan={e.details?.plan} />
                    </div>
                  ))}
                </div>
              )
            }
          </Load>
        </div>
      )}
    </Drawer>
  )
}

/** What one event says, by kind. */
function EventSummary({ e }: { e: LogEvent }) {
  const d = e.details ?? {}
  switch (e.kind) {
    case 'lock_wait':
      return (
        <span>
          {e.message} {d.acquired ? '(acquired)' : ''} · {ms(e.duration_ms ?? null)}
          {Array.isArray(d.holders) && d.holders.length > 0 && <span className="text-muted"> · held by pid {(d.holders as number[]).join(', ')}</span>}
        </span>
      )
    case 'deadlock':
      return (
        <span>
          Deadlock between pids {Array.isArray(d.cycle) ? (d.cycle as { pid: number }[]).map((c) => c.pid).join(' → ') : '?'}
        </span>
      )
    case 'temp_file':
      return <span>Temporary file of {bytes(typeof d.bytes === 'number' ? d.bytes : null)}</span>
    case 'autovacuum':
    case 'autoanalyze':
    case 'checkpoint':
      return (
        <span>
          {e.message} · {ms(e.duration_ms ?? null)}
        </span>
      )
    default:
      return (
        <span>
          {e.sqlstate && <span className="font-mono text-xs text-muted">{e.sqlstate} </span>}
          {e.message}
        </span>
      )
  }
}

const EVENT_TABS: { value: string; label: string; kinds: LogKind[] }[] = [
  { value: 'locks', label: 'Lock waits & deadlocks', kinds: ['lock_wait', 'deadlock'] },
  { value: 'maintenance', label: 'Autovacuum', kinds: ['autovacuum', 'autoanalyze'] },
  { value: 'temp', label: 'Temporary files', kinds: ['temp_file'] },
  { value: 'checkpoints', label: 'Checkpoints', kinds: ['checkpoint'] },
  { value: 'errors', label: 'Errors', kinds: ['error'] },
]

function Events({ id }: { id: number }) {
  const [range] = useRange()
  const [tab, setTab] = useState('locks')
  const kinds = EVENT_TABS.find((t) => t.value === tab)?.kinds ?? []
  const q = useLogEvents(id, kinds, range)
  return (
    <Card pad={false} title="Log events">
      <div className="px-4">
        <Tabs value={tab} onChange={setTab} items={EVENT_TABS.map((t) => ({ value: t.value, label: t.label }))} label="Event kind" />
      </div>
      <Load q={q} label="Loading events">
        {(d) =>
          d.events.length === 0 ? (
            <div className="p-4">
              <Empty title="None in this time range" />
            </div>
          ) : (
            <ul className="divide-y divide-line">
              {d.events.map((e) => (
                <li key={e.id} className="px-4 py-2 text-sm">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="num w-36 shrink-0 text-xs text-muted">{time(e.at, { date: true, seconds: true })}</span>
                    <Badge tone={e.kind === 'deadlock' || e.severity === 'PANIC' || e.severity === 'FATAL' ? 'crit' : e.kind === 'error' || e.kind === 'lock_wait' ? 'warn' : 'muted'}>
                      {KIND_LABEL[e.kind]}
                    </Badge>
                    <span className="min-w-0 flex-1">
                      <EventSummary e={e} />
                    </span>
                    <span className="text-xs text-muted">{[e.datname, e.username, e.pid ? `pid ${e.pid}` : null].filter(Boolean).join(' · ')}</span>
                  </div>
                  {e.query && <SqlText sql={e.query} clamp />}
                </li>
              ))}
            </ul>
          )
        }
      </Load>
    </Card>
  )
}

/** Events from the PostgreSQL server log of one instance (shipped by the log agent). */
export default function LogsPage() {
  const id = useNumParam('id')
  const [range] = useRange()
  const q = useLogSummary(id, range)
  const [open, setOpen] = useState<LogStatement | null>(null)
  const [from, to] = useMemo(() => rangeWindow(range), [range])
  return (
    <Load q={q} label="Loading the server log">
      {(d) => (
        <div className="space-y-3">
          {d.agents.length > 0 && d.counts.length === 0 && <Notice>The agent is connected, but no events were logged in this time range.</Notice>}
          <Setup d={d} now={Date.parse(d.to)} />
          {d.counts.length > 0 && (
            <Card title="Events over time">
              <TimeChart series={countSeries(d)} format={(n) => num(n)} from={from} to={to} label="Log events per kind" integer stacked={false} height={180} />
            </Card>
          )}
          <Statements d={d} onOpen={setOpen} />
          <Events id={id} />
          <StatementDrawer id={id} s={open} onClose={() => setOpen(null)} />
        </div>
      )}
    </Load>
  )
}
