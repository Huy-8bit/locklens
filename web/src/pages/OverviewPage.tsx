import { useMemo, useState } from 'react'
import { Link } from 'react-router'
import { Activity, ArrowRight, Database, GitBranch, Snail, TriangleAlert, Users } from 'lucide-react'
import { useFleetLive, useFleetMetrics, useFleetQueries, useIncidents } from '../api/hooks'
import type { FleetLiveTarget, FleetMetricsResponse, FleetQuery } from '../api/types'
import { slot } from '../components/MetricCard'
import { PageHeader } from '../components/Shell'
import { SqlText } from '../components/SqlText'
import { tableCls, tdCls, tdNum, Th, trCls } from '../components/Table'
import { TimeChart } from '../components/TimeChart'
import { Card, Delta, Empty, ErrorBox, KpiCard, Load, Meter, Pills, selectCls, Spinner } from '../components/ui'
import { BlockingGraph } from '../components/widgets/BlockingGraph'
import { ExplorerPanel, type ExplorerSelection } from '../components/widgets/ExplorerPanel'
import { InstanceSelect } from '../components/widgets/InstanceSelect'
import { SessionsTable } from '../components/widgets/SessionsTable'
import { SqlPanel } from '../components/widgets/SqlPanel'
import { lagHealth, TopologyGraph } from '../components/widgets/TopologyGraph'
import { combine, firstLast, fleetPoints, sumLatest, topology } from '../lib/fleet'
import { bytes, compact, ms, num, pct, rate } from '../lib/format'
import { rangeLabel, useScope, type Range } from '../lib/scope'

const RW = ['tuples.returned.per_s', 'tuples.fetched.per_s', 'tuples.inserted.per_s', 'tuples.updated.per_s', 'tuples.deleted.per_s', 'xact.commit.per_s', 'xact.rollback.per_s']
const LOAD = ['connections.used', 'statements.calls.per_s', 'statements.exec_ms.per_s']
const HOST = ['host.cpu_utilization_ratio', 'host.memory_total_bytes', 'host.memory_available_bytes', 'host.filesystem_used_ratio', 'replication.max_replay_lag_bytes']

const SLOW_MS = 1000

const sum = (v: number[]) => v.reduce((a, b) => a + b, 0)
const span = (d: FleetMetricsResponse) => ({ from: Date.parse(d.from), to: Date.parse(d.to) })
const lastKnown = (p: [number, number | null][]) => [...p].reverse().find((x) => x[1] !== null)?.[1] ?? null

/** Mean latency per bucket: execution time over calls (unknown when idle). */
function latencyPoints(d: FleetMetricsResponse) {
  return combine(d, ['statements.exec_ms.per_s', 'statements.calls.per_s'], ([t, c]) => (c && c > 0 ? (t ?? 0) / c : null))
}

// ---------------------------------------------------------------------------
// KPI row

function Kpis({ targets, load, env, range }: { targets: FleetLiveTarget[]; load: FleetMetricsResponse | undefined; env: string; range: Range }) {
  const slow = useFleetQueries(env, range, 'mean_time', 50)
  const incidents = useIncidents({ state: 'open' })

  const states = targets.reduce<Record<string, number>>((m, t) => ({ ...m, [t.target.status.state]: (m[t.target.status.state] ?? 0) + 1 }), {})
  const unhealthy = targets.length - (states.healthy ?? 0)

  const used = sumLatest(targets, 'connections.used')
  const max = sumLatest(targets, 'connections.max')
  const conn = load ? firstLast(fleetPoints(load, 'connections.used')) : [null, null]

  const qpsPts = load ? fleetPoints(load, 'statements.calls.per_s') : []
  const qps = lastKnown(qpsPts) ?? sumLatest(targets, 'statements.calls.per_s')
  const [qFirst, qLast] = firstLast(qpsPts)
  const latency = load ? lastKnown(latencyPoints(load)) : null

  const edges = topology(targets).groups.flatMap((g) => g.edges)
  const inSync = edges.filter((e) => lagHealth(e) === 'healthy').length

  const slowItems = (slow.data?.items ?? []).filter((q) => (q.mean_exec_ms ?? 0) > SLOW_MS)
  const ids = new Set(targets.map((t) => t.target.id))
  const open = (incidents.data?.incidents ?? []).filter((i) => !env || (i.target_id !== null && ids.has(i.target_id)))
  const openTotal = env ? open.length : (incidents.data?.total ?? open.length)
  const critical = open.filter((i) => i.severity === 'critical').length
  const warning = open.filter((i) => i.severity === 'warning').length

  return (
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-6">
      <KpiCard
        to="/databases"
        icon={<Database />}
        tone="blue"
        label="Databases monitored"
        value={num(targets.length)}
        sub={targets.length === 0 ? 'No enabled instance' : unhealthy === 0 ? 'All healthy' : Object.entries(states).filter(([s]) => s !== 'healthy').map(([s, n]) => `${n} ${s}`).join(', ')}
      />
      <KpiCard to="/connections" icon={<Users />} tone="violet" label="Active connections" value={num(used)} delta={<Delta from={conn[0]} to={conn[1]} />} sub={max !== null ? `Max ${num(max)}` : 'max_connections unknown'} />
      <KpiCard
        to="/queries"
        icon={<Activity />}
        tone="amber"
        label="Query throughput"
        value={compact(qps)}
        unit={qps !== null ? 'qps' : undefined}
        delta={<Delta from={qFirst} to={qLast} />}
        sub={latency !== null ? `Avg latency ${ms(latency)}` : 'Avg latency unknown'}
      />
      <KpiCard
        to="/topology"
        icon={<GitBranch />}
        tone="green"
        label="Replication health"
        value={edges.length ? `${inSync}/${edges.length}` : '—'}
        sub={edges.length === 0 ? 'No replication observed' : inSync === edges.length ? 'All standbys in sync' : `${edges.length - inSync} with lag`}
      />
      <KpiCard
        to="/queries?sort=mean_time"
        icon={<Snail />}
        tone="red"
        label="Slow queries"
        value={slow.data ? `${slowItems.length}${slowItems.length === 50 ? '+' : ''}` : '—'}
        sub={`Mean > ${ms(SLOW_MS)} · ${rangeLabel(range).toLowerCase()}`}
      />
      <KpiCard
        to="/incidents"
        icon={<TriangleAlert />}
        tone="red"
        label="Incidents"
        value={incidents.data ? num(openTotal) : '—'}
        sub={openTotal === 0 ? 'None open' : `${critical} critical, ${warning} warning`}
      />
    </div>
  )
}

// ---------------------------------------------------------------------------
// Charts

type RwMode = 'rows' | 'xact'

function ReadWriteCard({ d }: { d: FleetMetricsResponse | undefined }) {
  const [mode, setMode] = useState<RwMode>('rows')
  const series = useMemo(() => {
    if (!d) return []
    if (mode === 'xact')
      return [
        { key: 'commit', label: 'Commits', color: slot(0), points: fleetPoints(d, 'xact.commit.per_s') },
        { key: 'rollback', label: 'Rollbacks', color: slot(1), points: fleetPoints(d, 'xact.rollback.per_s') },
      ]
    return [
      { key: 'read', label: 'Reads', color: slot(0), points: combine(d, ['tuples.returned.per_s', 'tuples.fetched.per_s'], sum) },
      { key: 'write', label: 'Writes', color: slot(1), points: combine(d, ['tuples.inserted.per_s', 'tuples.updated.per_s', 'tuples.deleted.per_s'], sum) },
    ]
  }, [d, mode])
  return (
    <Card
      title="Read / write operations"
      subtitle={mode === 'rows' ? 'Rows read (returned + fetched) and written (inserted + updated + deleted)' : 'Transactions committed and rolled back'}
      actions={
        <>
          <label htmlFor="rw-mode" className="sr-only">
            Unit
          </label>
          <select id="rw-mode" className={`${selectCls} py-1 text-xs`} value={mode} onChange={(e) => setMode(e.target.value as RwMode)}>
            <option value="rows">Rows/s</option>
            <option value="xact">Transactions/s</option>
          </select>
        </>
      }
    >
      {d ? <TimeChart label="Read / write operations" series={series} format={(n) => rate(n)} height={200} area {...span(d)} /> : <ChartLoading h={230} />}
    </Card>
  )
}

function ThroughputCard({ d }: { d: FleetMetricsResponse | undefined }) {
  const qps = useMemo(() => (d ? [{ key: 'qps', label: 'Queries per second', color: slot(0), points: fleetPoints(d, 'statements.calls.per_s'), bars: true }] : []), [d])
  const lat = useMemo(() => (d ? [{ key: 'lat', label: 'Mean latency', color: slot(2), points: latencyPoints(d) }] : []), [d])
  return (
    <Card title="Query throughput & latency" subtitle="Statement executions per second, and mean execution time per statement (pg_stat_statements)">
      {d ? (
        <div>
          <p className="mb-1 flex items-center gap-1.5 text-xs text-ink-2">
            <span aria-hidden className="h-2.5 w-2.5 rounded-sm" style={{ background: slot(0) }} /> Queries per second
          </p>
          <TimeChart label="Queries per second" series={qps} format={(n) => compact(n)} height={110} compactAxis axisWidth={56} {...span(d)} />
          <p className="mt-2 mb-1 flex items-center gap-1.5 text-xs text-ink-2">
            <span aria-hidden className="h-0.5 w-3 rounded" style={{ background: slot(2) }} /> Mean latency
          </p>
          <TimeChart label="Mean latency" series={lat} format={(n) => ms(n)} height={110} axisWidth={56} {...span(d)} />
        </div>
      ) : (
        <ChartLoading h={250} />
      )}
    </Card>
  )
}

function ChartLoading({ h }: { h: number }) {
  return (
    <div style={{ height: h }}>
      <Spinner label="Loading chart" />
    </div>
  )
}

/** Host resources from node_exporter, one row per distinct exporter. */
function ResourcesCard({ targets, d }: { targets: FleetLiveTarget[]; d: FleetMetricsResponse | undefined }) {
  const hosts = useMemo(() => {
    const seen = new Map<string, NonNullable<FleetLiveTarget['host']>>()
    for (const t of targets) if (t.host && t.target.node_exporter_url && !seen.has(t.target.node_exporter_url)) seen.set(t.target.node_exporter_url, t.host)
    return [...seen.values()]
  }, [targets])
  const series = useMemo(
    () =>
      d
        ? [
            { key: 'cpu', label: 'CPU', color: slot(0), points: fleetPoints(d, 'host.cpu_utilization_ratio') },
            { key: 'mem', label: 'Memory', color: slot(1), points: combine(d, ['host.memory_total_bytes', 'host.memory_available_bytes'], ([t, a]) => (t ? 1 - (a ?? 0) / t : null)) },
            { key: 'disk', label: 'Disk used', color: slot(2), points: fleetPoints(d, 'host.filesystem_used_ratio') },
          ]
        : [],
    [d],
  )
  if (hosts.length === 0) {
    return (
      <Card title="System resources">
        <Empty title="No host metrics">Set a node_exporter URL on an instance to see CPU, memory, and disk of its host.</Empty>
      </Card>
    )
  }
  const cpus = hosts.map((h) => h.cpu_utilization_ratio).filter((v): v is number => v !== null)
  // CPU is a rate between two scrapes: right after a restart the latest
  // sample has none yet, so fall back to the last known bucket.
  const cpu = cpus.length ? sum(cpus) / cpus.length : d ? lastKnown(fleetPoints(d, 'host.cpu_utilization_ratio')) : null
  const total = sum(hosts.map((h) => h.memory_total_bytes ?? 0))
  const avail = sum(hosts.map((h) => h.memory_available_bytes ?? 0))
  const mem = total > 0 ? 1 - avail / total : null
  const fs = hosts.flatMap((h) => h.filesystems ?? []).filter((f) => f.size_bytes > 0)
  const disk = fs.length ? Math.max(...fs.map((f) => 1 - f.avail_bytes / f.size_bytes)) : null
  const ioKnown = hosts.some((h) => h.disk_read_bytes_per_second !== null)
  const read = sum(hosts.map((h) => h.disk_read_bytes_per_second ?? 0))
  const write = sum(hosts.map((h) => h.disk_write_bytes_per_second ?? 0))
  const tone = (v: number | null) => (v === null ? undefined : v >= 0.9 ? 'crit' : v >= 0.75 ? 'warn' : undefined)
  const items: [string, number | null, string][] = [
    ['CPU', cpu, 'busy'],
    ['Memory', mem, total > 0 ? `of ${bytes(total)}` : 'unknown'],
    ['Disk', disk, 'fullest filesystem'],
  ]
  return (
    <Card title="System resources" subtitle={`${hosts.length} host${hosts.length === 1 ? '' : 's'} from node_exporter`}>
      <div className="grid grid-cols-3 gap-4">
        {items.map(([label, v, sub]) => (
          <div key={label} className="min-w-0">
            <div className="text-xs font-medium text-ink-2">{label}</div>
            <div className="text-xl font-bold text-ink">{pct(v, 0)}</div>
            <Meter value={v} max={1} tone={tone(v)} />
            <div className="mt-1 truncate text-[11px] text-muted">{sub}</div>
          </div>
        ))}
      </div>
      <div className="mt-3">{d ? <TimeChart label="Host utilization" series={series} format={(n) => pct(n, 0)} height={120} {...span(d)} /> : <ChartLoading h={150} />}</div>
      <p className="mt-2 text-xs text-muted">{ioKnown ? `Disk I/O: read ${bytes(read)}/s · write ${bytes(write)}/s` : 'Disk I/O unavailable'}</p>
    </Card>
  )
}

// ---------------------------------------------------------------------------
// Top queries

type Sort = 'total_time' | 'calls' | 'mean_time' | 'rows'

const SORT_VALUE: Record<Sort, (q: FleetQuery) => number | null> = {
  total_time: (q) => q.total_exec_ms,
  calls: (q) => q.calls,
  mean_time: (q) => q.mean_exec_ms,
  rows: (q) => q.rows,
}

function TopQueriesCard({ env, range, multi }: { env: string; range: Range; multi: boolean }) {
  const [sort, setSort] = useState<Sort>('total_time')
  const q = useFleetQueries(env, range, sort, 6)
  return (
    <Card
      pad={false}
      title="Top queries"
      subtitle={`Across instances · ${rangeLabel(range).toLowerCase()}`}
      actions={
        <Link to="/queries" className="inline-flex items-center gap-1 text-xs font-medium text-accent hover:underline">
          All queries <ArrowRight aria-hidden className="h-3 w-3" />
        </Link>
      }
    >
      <div className="px-5 pb-3">
        <Pills label="Rank queries by" value={sort} onChange={setSort} items={[{ value: 'total_time', label: 'Total time' }, { value: 'calls', label: 'Calls' }, { value: 'mean_time', label: 'Mean latency' }, { value: 'rows', label: 'Rows' }]} />
      </div>
      <Load q={q} label="Loading queries">
        {(d) => {
          if (d.items.length === 0) return <div className="px-5 pb-5"><Empty title="No statements in this range">Statement statistics come from pg_stat_statements.</Empty></div>
          const top = Math.max(...d.items.map((i) => SORT_VALUE[sort](i) ?? 0), 0)
          return (
            <div className="overflow-x-auto">
              <table className={tableCls}>
                <caption className="sr-only">Top queries</caption>
                <thead>
                  <tr>
                    <Th className="pl-5">Query</Th>
                    <Th align="right">Calls</Th>
                    <Th align="right">Total</Th>
                    <Th align="right" className="pr-5">
                      {sort === 'rows' ? 'Rows' : 'Mean'}
                    </Th>
                  </tr>
                </thead>
                <tbody>
                  {d.items.map((it) => {
                    const v = SORT_VALUE[sort](it) ?? 0
                    return (
                      <tr key={`${it.target_id}-${it.identity_id}`} className={trCls}>
                        <td className={`${tdCls} max-w-[220px] pl-5`}>
                          <Link to={`/queries/${it.target_id}/${it.identity_id}`} className="block hover:underline">
                            {it.query_text ? <SqlText sql={it.query_text} clamp /> : <span className="font-mono text-xs text-muted">queryid {it.queryid}</span>}
                          </Link>
                          <div className="mt-1 flex items-center gap-2">
                            <span className="h-1.5 flex-1 overflow-hidden rounded-full bg-surface-3" title={`Share of the top value by ${sort.replace('_', ' ')}`}>
                              <span className="block h-full rounded-full" style={{ width: `${top > 0 ? Math.max(2, (v / top) * 100) : 0}%`, background: slot(0) }} />
                            </span>
                            {multi && <span className="shrink-0 text-[11px] text-muted">{it.target_name}</span>}
                          </div>
                        </td>
                        <td className={tdNum}>{compact(it.calls)}</td>
                        <td className={tdNum}>{ms(it.total_exec_ms)}</td>
                        <td className={`${tdNum} pr-5`}>{sort === 'rows' ? compact(it.rows) : ms(it.mean_exec_ms)}</td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          )
        }}
      </Load>
    </Card>
  )
}

// ---------------------------------------------------------------------------

function MoreLink({ to, children }: { to: string; children: string }) {
  return (
    <Link to={to} className="inline-flex items-center gap-1 text-xs font-medium text-accent hover:underline">
      {children} <ArrowRight aria-hidden className="h-3 w-3" />
    </Link>
  )
}

export default function OverviewPage() {
  const { env, range } = useScope()
  const live = useFleetLive(env)
  const rw = useFleetMetrics(env, RW, range)
  const load = useFleetMetrics(env, LOAD, range)
  const host = useFleetMetrics(env, HOST, range)
  const targets = useMemo(() => live.data?.targets ?? [], [live.data])
  const [explorerId, setExplorerId] = useState(0)
  const [sqlId, setSqlId] = useState(0)
  const [selection, setSelection] = useState<ExplorerSelection | null>(null)
  const list = targets.map((t) => t.target)
  const pick = (id: number) => (list.some((t) => t.id === id) ? id : (list[0]?.id ?? 0))
  const exId = pick(explorerId)
  const sId = pick(sqlId)
  const blocked = targets.reduce((n, t) => n + (t.report?.analysis.summary.blocked_sessions ?? 0), 0)

  return (
    <>
      <PageHeader title="Overview" subtitle={`${env ? `${env.charAt(0).toUpperCase()}${env.slice(1)} environment` : 'All environments'} · ${targets.length} instance${targets.length === 1 ? '' : 's'} · ${rangeLabel(range).toLowerCase()}`} />
      {live.error && !live.data ? (
        <ErrorBox error={live.error} title="Could not load the fleet" />
      ) : !live.data ? (
        <Spinner label="Loading instances" />
      ) : targets.length === 0 ? (
        <Empty title={env ? `No enabled instance in “${env}”` : 'No instance is monitored yet'}>
          <Link to="/settings/targets/new" className="text-accent hover:underline">
            Add a PostgreSQL instance
          </Link>{' '}
          to start monitoring.
        </Empty>
      ) : (
        <div className="space-y-4">
          <Kpis targets={targets} load={load.data} env={env} range={range} />
          <div className="grid gap-4 lg:grid-cols-2 2xl:grid-cols-3">
            <ReadWriteCard d={rw.data} />
            <ThroughputCard d={load.data} />
            <ResourcesCard targets={targets} d={host.data} />
          </div>
          <div className="grid gap-4 lg:grid-cols-2 2xl:grid-cols-3">
            <Card title="Database topology" subtitle="Primaries and the standbys streaming from them" actions={<MoreLink to="/topology">View topology</MoreLink>}>
              <TopologyGraph targets={targets} compact />
              {host.data && topology(targets).groups.some((g) => g.edges.length > 0) && (
                <div className="mt-5 border-t border-line pt-4">
                  <p className="mb-1 text-xs font-medium text-ink-2">Largest replay lag (WAL not yet replayed)</p>
                  <TimeChart label="Largest replay lag" series={[{ key: 'lag', label: 'Replay lag', color: slot(0), points: fleetPoints(host.data, 'replication.max_replay_lag_bytes') }]} format={(n) => bytes(n)} height={130} area integer {...span(host.data)} />
                </div>
              )}
            </Card>
            <TopQueriesCard env={env} range={range} multi={targets.length > 1} />
            <Card pad={false} title="Active sessions" subtitle="Running a query or holding a transaction, longest first" actions={<MoreLink to="/sessions">View all sessions</MoreLink>} className="lg:col-span-2 2xl:col-span-1">
              <SessionsTable targets={targets} compact limit={6} />
            </Card>
          </div>
          <div className="grid gap-4 xl:grid-cols-2 2xl:grid-cols-3">
            <Card title="Blocking transactions" subtitle={blocked ? `${blocked} session${blocked === 1 ? '' : 's'} waiting on a lock held by another session` : 'From pg_blocking_pids() in the latest sample'} actions={<MoreLink to="/blocking">Details</MoreLink>}>
              <BlockingGraph targets={targets} max={2} />
            </Card>
            <Card pad={false} title="Database explorer" actions={<MoreLink to={exId ? `/explorer/${exId}` : '/explorer'}>Open explorer</MoreLink>}>
              {exId > 0 && (
                <ExplorerPanel
                  key={exId}
                  id={exId}
                  compactView
                  selection={selection}
                  onSelect={setSelection}
                  toolbar={
                    <InstanceSelect
                      targets={list}
                      value={exId}
                      onChange={(id) => {
                        setExplorerId(id)
                        setSelection(null)
                      }}
                    />
                  }
                />
              )}
            </Card>
            <Card pad={false} title="SQL editor" subtitle="Read-only, separate role" actions={<MoreLink to={sId ? `/sql/${sId}` : '/sql'}>Open editor</MoreLink>} className="xl:col-span-2 2xl:col-span-1">
              {sId > 0 && <SqlPanel key={sId} id={sId} compactView toolbar={<InstanceSelect targets={list} value={sId} onChange={setSqlId} />} />}
            </Card>
          </div>
        </div>
      )}
    </>
  )
}
