import { useMemo } from 'react'
import { Link, NavLink, Outlet } from 'react-router'
import { Activity, ArrowRight, Database, Gauge, HardDrive, Lock, Timer, Users, Zap } from 'lucide-react'
import { useActivity, useCommands, useOverview, useQueries, useTarget } from '../api/hooks'
import type { OverviewResponse } from '../api/types'
import { CommandMix, CommandsChart } from '../components/Commands'
import { MetricCard, slot } from '../components/MetricCard'
import { PageHeader, useNumParam } from '../components/Shell'
import { SqlText } from '../components/SqlText'
import { Badge, Card, cx, ErrorBox, Load, Meter, Notice, SeverityBadge, Spinner, Stat, StateBadge } from '../components/ui'
import { commandColor, COMMAND_LABEL } from '../lib/commands'
import { ageOf, bytes, compact, ms, num, pct, rate, seconds } from '../lib/format'
import { useRange } from '../lib/scope'
import { useCan } from '../auth'

function latest(o: OverviewResponse, k: string): number | null {
  return o.latest[k]?.value ?? null
}

function Kpis({ o }: { o: OverviewResponse }) {
  const v = (k: string) => latest(o, k)
  const used = v('connections.used')
  const max = v('connections.max')
  const usage = v('connections.usage_ratio')
  const calls = v('statements.calls.per_s')
  const execMs = v('statements.exec_ms.per_s')
  const commits = v('xact.commit.per_s')
  const rollbacks = v('xact.rollback.per_s')
  const tps = commits !== null && rollbacks !== null ? commits + rollbacks : null
  const read = v('tuples.returned.per_s') !== null || v('tuples.fetched.per_s') !== null ? (v('tuples.returned.per_s') ?? 0) + (v('tuples.fetched.per_s') ?? 0) : null
  const written = ['tuples.inserted.per_s', 'tuples.updated.per_s', 'tuples.deleted.per_s'].some((k) => v(k) !== null)
    ? (v('tuples.inserted.per_s') ?? 0) + (v('tuples.updated.per_s') ?? 0) + (v('tuples.deleted.per_s') ?? 0)
    : null
  const blocked = v('blocking.blocked_sessions')
  const oldest = v('transactions.oldest_age_seconds')
  const standby = o.target.status.is_standby
  const lag = standby ? v('standby.replay_delay_bytes') : v('replication.max_replay_lag_bytes')
  const usageTone = usage === null ? undefined : usage >= 0.95 ? 'crit' : usage >= 0.8 ? 'warn' : undefined

  return (
    <>
      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
        <div className="rounded-xl border border-line bg-surface px-4 py-3">
          <div className="flex items-center gap-1.5 text-xs font-medium text-muted">
            <Users aria-hidden className="h-3.5 w-3.5" />
            Connections
          </div>
          <div className="mt-1 text-2xl font-semibold tracking-tight">
            {num(used)}
            <span className="text-base font-normal text-muted"> / {num(max)}</span>
          </div>
          <Meter value={used} max={max} tone={usageTone} />
          <div className="mt-1 text-xs text-muted">{pct(usage, 0)} of available slots</div>
        </div>
        <Stat icon={<Activity aria-hidden className="h-3.5 w-3.5" />} label="Running now" value={num(v('sessions.active'))} sub={`${num(v('sessions.idle_in_transaction'))} idle in transaction · ${num(v('sessions.waiting_lock'))} waiting`} />
        <Stat icon={<Zap aria-hidden className="h-3.5 w-3.5" />} label="Queries per second" value={rate(calls, '')} sub={calls && execMs !== null ? `avg ${ms(execMs / calls)} per query` : 'from pg_stat_statements'} />
        <Stat icon={<Gauge aria-hidden className="h-3.5 w-3.5" />} label="Transactions per second" value={rate(tps, '')} sub={`${rate(rollbacks)} rollbacks`} />
        <Stat icon={<HardDrive aria-hidden className="h-3.5 w-3.5" />} label="Cache hit ratio" value={pct(v('cache.hit_ratio'))} sub="shared buffers" />
        <Stat icon={<Database aria-hidden className="h-3.5 w-3.5" />} label="Database size" value={bytes(v('db.total_size_bytes'))} sub="all databases" />
      </div>
      <div className="mt-3 grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
        <Stat icon={<Lock aria-hidden className="h-3.5 w-3.5" />} label="Blocked sessions" value={num(blocked)} tone={blocked ? 'crit' : undefined} sub={blocked ? `longest wait ${seconds(v('blocking.max_lock_wait_seconds'))}` : 'no lock waits'} />
        <Stat icon={<Timer aria-hidden className="h-3.5 w-3.5" />} label="Oldest transaction" value={oldest === null ? 'none open' : seconds(oldest)} tone={oldest !== null && oldest > 300 ? 'warn' : undefined} sub={`${num(v('transactions.long'))} long-running`} />
        <Stat label="Rows read per second" value={rate(read, '')} sub="returned + fetched" />
        <Stat label="Rows written per second" value={rate(written, '')} sub="inserted + updated + deleted" />
        <Stat label="Temp files written" value={`${bytes(v('temp.bytes.per_s'))}/s`} sub="sorts and hashes spilling to disk" />
        <Stat label={standby ? 'Replay delay' : 'Replica lag'} value={bytes(lag)} sub={standby ? 'received, not yet replayed' : 'largest replay lag'} />
      </div>
    </>
  )
}

function LongestRunning({ id }: { id: number }) {
  const q = useActivity(id)
  const rows = useMemo(() => {
    const r = q.data?.report
    if (!r) return []
    const now = r.snapshot.server_time
    return r.snapshot.sessions
      .filter((s) => s.state === 'active' && s.backend_type === 'client backend')
      .map((s) => ({ s, age: ageOf(s.query_start, now) }))
      .sort((a, b) => (b.age ?? 0) - (a.age ?? 0))
      .slice(0, 5)
  }, [q.data])
  return (
    <Card title="Running right now" subtitle="Longest-running active queries from the latest activity sample" actions={<Link to={`/databases/${id}/activity`} className="text-xs text-accent hover:underline">Live activity</Link>}>
      <Load q={q}>
        {() =>
          rows.length === 0 ? (
            <p className="text-sm text-muted">No client query is running at this moment.</p>
          ) : (
            <ul className="space-y-2.5">
              {rows.map(({ s, age }) => (
                <li key={s.pid} className="text-sm">
                  <div className="flex items-center justify-between gap-2 text-xs text-muted">
                    <span className="truncate">
                      pid {s.pid} · {s.user ?? '?'} · {s.application_name || 'no application name'}
                    </span>
                    <span className="num shrink-0 font-medium text-ink">{seconds(age)}</span>
                  </div>
                  {s.query ? <SqlText sql={s.query} clamp /> : <span className="text-xs text-muted">query text not available</span>}
                </li>
              ))}
            </ul>
          )
        }
      </Load>
    </Card>
  )
}

function TopQueries({ id, range }: { id: number; range: ReturnType<typeof useRange>[0] }) {
  const q = useQueries(id, { range, sort: 'total_time', order: 'desc', page: 1, q: '', command: '', compare: false, includeMonitoring: false, pageSize: 5 })
  return (
    <Card title="Most expensive queries" subtitle="By total execution time in the selected range" actions={<Link to={`/databases/${id}/queries`} className="text-xs text-accent hover:underline">All queries</Link>}>
      <Load q={q}>
        {(d) =>
          d.available === false ? (
            <p className="text-sm text-muted">{d.unavailable_reason || 'pg_stat_statements is not available.'}</p>
          ) : d.items.length === 0 ? (
            <p className="text-sm text-muted">No statements in this range yet.</p>
          ) : (
            <ul className="space-y-2.5">
              {d.items.map((it) => (
                <li key={it.identity_id}>
                  <Link to={`/queries/${id}/${it.identity_id}`} className="block rounded-md hover:bg-surface-2">
                    <div className="flex items-center justify-between gap-2 text-xs text-muted">
                      <span className="inline-flex items-center gap-1.5">
                        <span aria-hidden className="h-2 w-2 rounded-sm" style={{ background: commandColor(it.command) }} />
                        {COMMAND_LABEL[it.command] ?? it.command} · {compact(it.calls)} calls · avg {ms(it.mean_exec_ms)}
                      </span>
                      <span className="num shrink-0 font-medium text-ink">{pct(it.share_of_total_time, 0)}</span>
                    </div>
                    {it.query_text ? <SqlText sql={it.query_text} clamp /> : <span className="font-mono text-xs text-muted">queryid {it.queryid}</span>}
                  </Link>
                </li>
              ))}
            </ul>
          )
        }
      </Load>
    </Card>
  )
}

/** Overview of one instance: what it is doing now and how it got here. */
export function DatabaseOverview() {
  const id = useNumParam('id')
  const [range] = useRange()
  const o = useOverview(id)
  const commands = useCommands(id, range)

  if (!o.data) return o.error ? <ErrorBox error={o.error} title="Could not load this instance" /> : <Spinner label="Loading instance" />
  const d = o.data
  const t = d.target
  const restricted = d.visibility_warnings?.some((w) => w.code === 'restricted_visibility')

  return (
    <>
      <div className="space-y-3">
        {!d.connected && d.connect_error && <Notice tone="crit">Not connected: {d.connect_error}. Values below may be stale.</Notice>}
        {restricted && <Notice tone="warn">The monitoring role cannot see other users' sessions in full (grant pg_read_all_stats). Counts and queries may be incomplete.</Notice>}
        {d.open_incident_count > 0 && (
          <Notice tone="crit">
            <span className="font-medium">{d.open_incident_count} open incident(s):</span> {d.open_incidents.map((i) => i.title).join('; ')}.{' '}
            <Link to={`/databases/${id}/alerts`} className="text-accent underline">
              View alerts
            </Link>
          </Notice>
        )}

        <Kpis o={d} />

        <div className="grid gap-3 xl:grid-cols-3">
          <div className="space-y-3 xl:col-span-2">
            <Card title="Queries by type" subtitle="Executions per second of each kind of statement (pg_stat_statements)">
              <Load q={commands} label="Loading workload">
                {(c) => (c.available === false ? <p className="text-sm text-muted">{c.unavailable_reason || 'pg_stat_statements is not available on this instance.'}</p> : <CommandsChart data={c} />)}
              </Load>
            </Card>
            <div className="grid gap-3 md:grid-cols-2">
              <MetricCard id={id} range={range} title="Transactions per second" format={(n) => rate(n)} height={180} defs={[{ metric: 'xact.commit.per_s', label: 'Commits', color: slot(0) }, { metric: 'xact.rollback.per_s', label: 'Rollbacks', color: slot(1) }]} />
              <MetricCard id={id} range={range} title="Connections by state" format={(n) => num(n, 1)} height={180} defs={[{ metric: 'sessions.active', label: 'Active', color: slot(0) }, { metric: 'sessions.idle', label: 'Idle', color: slot(1) }, { metric: 'sessions.idle_in_transaction', label: 'Idle in transaction', color: slot(2) }, { metric: 'sessions.waiting_lock', label: 'Waiting for a lock', color: slot(3) }]} />
              <MetricCard id={id} range={range} title="Rows read" subtitle="Tuples returned by scans and fetched by index lookups" format={(n) => rate(n)} height={180} defs={[{ metric: 'tuples.returned.per_s', label: 'Returned', color: slot(0) }, { metric: 'tuples.fetched.per_s', label: 'Fetched', color: slot(1) }]} />
              <MetricCard id={id} range={range} title="Rows written" format={(n) => rate(n)} height={180} defs={[{ metric: 'tuples.inserted.per_s', label: 'Inserted', color: slot(1) }, { metric: 'tuples.updated.per_s', label: 'Updated', color: slot(2) }, { metric: 'tuples.deleted.per_s', label: 'Deleted', color: slot(3) }]} />
              <MetricCard id={id} range={range} title="Time spent executing queries" subtitle="Milliseconds of statement execution per second (includes waiting)" format={(n) => `${ms(n)}/s`} height={180} defs={[{ metric: 'statements.exec_ms.per_s', label: 'Execution time', color: slot(0) }]} />
              <MetricCard id={id} range={range} title="Cache hit ratio" format={(n) => pct(n, 1)} height={180} defs={[{ metric: 'cache.hit_ratio', label: 'Hit ratio', color: slot(0) }]} />
              <MetricCard id={id} range={range} title={t.status.is_standby ? 'WAL received' : 'WAL generated'} format={(n) => `${bytes(n)}/s`} height={180} defs={[{ metric: t.status.is_standby ? 'wal.received_bytes.per_s' : 'wal.bytes.per_s', label: 'WAL', color: slot(0) }]} />
              <MetricCard id={id} range={range} title="Temporary files" format={(n) => `${bytes(n)}/s`} height={180} defs={[{ metric: 'temp.bytes.per_s', label: 'Temp bytes', color: slot(0) }]} />
            </div>
          </div>
          <div className="space-y-3">
            <Card title="Query mix" subtitle="Share of executions and time by statement type in the selected range">
              <Load q={commands} label="Loading workload">
                {(c) => (c.available === false ? <p className="text-sm text-muted">Unavailable.</p> : <CommandMix data={c} compactView />)}
              </Load>
            </Card>
            <LongestRunning id={id} />
            <TopQueries id={id} range={range} />
            <Card title="Alerts" actions={<Link to={`/databases/${id}/alerts`} className="inline-flex items-center gap-1 text-xs text-accent hover:underline">Details <ArrowRight aria-hidden className="h-3 w-3" /></Link>}>
              {d.findings.filter((f) => f.finding.status === 'active').length === 0 ? (
                <p className="text-sm text-muted">No active findings.</p>
              ) : (
                <ul className="space-y-2">
                  {d.findings
                    .filter((f) => f.finding.status === 'active')
                    .slice(0, 5)
                    .map((f) => (
                      <li key={f.id} className="flex items-start gap-2 text-sm">
                        <SeverityBadge severity={f.finding.severity} />
                        <span className="min-w-0">{f.finding.title}</span>
                      </li>
                    ))}
                </ul>
              )}
            </Card>
            <Card title="Collection">
              <ul className="space-y-1 text-sm">
                {Object.values(t.status.collectors)
                  .sort((a, b) => a.name.localeCompare(b.name))
                  .map((c) => (
                    <li key={c.name} className="flex items-center justify-between gap-2">
                      <span>{c.name}</span>
                      {c.enabled ? (
                        c.last_error ? (
                          <Badge tone="crit" title={c.last_error}>failing</Badge>
                        ) : (
                          <span className="text-xs text-muted">every {seconds(c.interval_seconds)}</span>
                        )
                      ) : (
                        <Badge title={c.disabled_reason}>unavailable</Badge>
                      )}
                    </li>
                  ))}
              </ul>
            </Card>
          </div>
        </div>
      </div>
    </>
  )
}

const TABS: [string, string][] = [
  ['', 'Overview'],
  ['queries', 'Queries'],
  ['activity', 'Activity'],
  ['replication', 'Replication'],
  ['alerts', 'Alerts'],
]

/** One instance: its name and state, and tabs for its detail views. */
export default function DatabasePage() {
  const id = useNumParam('id')
  const can = useCan()
  const t = useTarget(id)
  if (!t.data) return t.error ? <ErrorBox error={t.error} title="Could not load this instance" /> : <Spinner label="Loading instance" />
  const tg = t.data
  return (
    <>
      <Link to="/databases" className="mb-2 inline-flex items-center gap-1 text-sm text-accent hover:underline">
        ← Databases
      </Link>
      <PageHeader
        title={
          <span className="flex flex-wrap items-center gap-2">
            {tg.name} <StateBadge state={tg.status.state} />
          </span>
        }
        subtitle={[tg.description, `${tg.host}:${tg.port}/${tg.database}`, tg.status.server_version ? `PostgreSQL ${tg.status.server_version}` : null, tg.status.is_standby === null ? null : tg.status.is_standby ? 'standby' : 'primary', tg.environment].filter(Boolean).join(' · ')}
        actions={
          <>
            <Link to={`/explorer/${id}`} className="rounded-lg border border-line-strong bg-surface px-3 py-1.5 text-sm font-medium text-ink hover:bg-surface-2">
              Explore data
            </Link>
            {can('workspace.sql') && (
              <Link to={`/sql/${id}`} className="rounded-lg border border-line-strong bg-surface px-3 py-1.5 text-sm font-medium text-ink hover:bg-surface-2">
                SQL editor
              </Link>
            )}
          </>
        }
      />
      <nav aria-label="Instance views" className="mb-5 flex gap-1 overflow-x-auto border-b border-line">
        {TABS.map(([path, label]) => (
          <NavLink
            key={path}
            to={path ? `/databases/${id}/${path}` : `/databases/${id}`}
            end
            className={({ isActive }) => cx('-mb-px whitespace-nowrap border-b-2 px-3 py-2 text-sm font-medium', isActive ? 'border-accent text-ink' : 'border-transparent text-muted hover:text-ink')}
          >
            {label}
          </NavLink>
        ))}
      </nav>
      <Outlet />
    </>
  )
}
