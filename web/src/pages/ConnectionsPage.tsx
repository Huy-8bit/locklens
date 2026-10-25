import { useMemo, useState } from 'react'
import { Link } from 'react-router'
import { Search } from 'lucide-react'
import { useFleetLive, useFleetMetrics } from '../api/hooks'
import { slot } from '../components/MetricCard'
import { PageHeader } from '../components/Shell'
import { tableCls, tdCls, tdNum, Th, trCls } from '../components/Table'
import { TimeChart } from '../components/TimeChart'
import { Badge, Card, Empty, inputCls, Load, Meter, Spinner } from '../components/ui'
import { stateTone } from '../components/widgets/SessionsTable'
import { fleetPoints } from '../lib/fleet'
import { num, pct, seconds } from '../lib/format'
import { useScope } from '../lib/scope'

const STATES = [
  ['sessions.active', 'Active'],
  ['sessions.idle', 'Idle'],
  ['sessions.idle_in_transaction', 'Idle in transaction'],
  ['sessions.waiting_lock', 'Waiting for a lock'],
] as const

export default function ConnectionsPage() {
  const { env, range } = useScope()
  const live = useFleetLive(env)
  const hist = useFleetMetrics(env, STATES.map(([m]) => m), range)
  const [term, setTerm] = useState('')
  const series = useMemo(() => {
    const h = hist.data
    return h ? STATES.map(([m, label], i) => ({ key: m, label, color: slot(i), points: fleetPoints(h, m) })) : []
  }, [hist.data])

  return (
    <>
      <PageHeader title="Connections" subtitle="Connection usage per instance, and who is connected: client sessions grouped by database, user, application, client address, and state." />
      <Load q={live} label="Loading connections">
        {(d) => {
          if (d.targets.length === 0) return <Empty title="No enabled instance in this environment" />
          const t = term.trim().toLowerCase()
          const groups = d.targets
            .flatMap((x) => x.connection_groups.map((g) => ({ x, g })))
            .filter(({ x, g }) => !t || [x.target.name, g.database, g.user, g.application, g.client_addr, g.state].some((v) => v?.toLowerCase().includes(t)))
            .sort((a, b) => b.g.count - a.g.count)
          return (
            <div className="space-y-4">
              <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
                {d.targets.map((x) => {
                  const used = x.counts?.used_connections ?? x.latest['connections.used']?.value ?? null
                  const max = x.counts?.max_connections ?? x.latest['connections.max']?.value ?? null
                  const reserved = x.counts?.reserved_connections ?? null
                  const ratio = used !== null && max ? used / max : null
                  return (
                    <Link key={x.target.id} to={`/databases/${x.target.id}/activity`} className="rounded-2xl border border-line bg-surface p-4 shadow-card hover:border-line-strong">
                      <div className="flex items-center justify-between gap-2 text-sm font-semibold text-ink">
                        <span className="truncate">{x.target.name}</span>
                        <span className="num text-xs font-medium text-ink-2">{pct(ratio, 0)}</span>
                      </div>
                      <div className="mt-1 text-2xl font-bold text-ink">
                        {num(used)}
                        <span className="text-base font-normal text-muted"> / {num(max)}</span>
                      </div>
                      <Meter value={used} max={max} tone={ratio === null ? undefined : ratio >= 0.95 ? 'crit' : ratio >= 0.8 ? 'warn' : undefined} />
                      <p className="mt-1.5 text-xs text-muted">
                        {num(x.counts?.active)} active · {num(x.counts?.idle)} idle · {num(x.counts?.idle_in_transaction)} idle in transaction{reserved ? ` · ${num(reserved)} reserved for superusers` : ''}
                      </p>
                    </Link>
                  )
                })}
              </div>
              <Card title="Client sessions by state" subtitle="All instances, stacked">
                {hist.data ? <TimeChart label="Client sessions by state" series={series} format={(n) => num(n, 1)} height={200} stacked integer from={Date.parse(hist.data.from)} to={Date.parse(hist.data.to)} /> : <Spinner label="Loading chart" />}
              </Card>
              <Card pad={false} title="Who is connected" subtitle="From pg_stat_activity in the latest sample; LockLens's own connections are excluded" actions={
                <div className="relative">
                  <Search aria-hidden className="pointer-events-none absolute top-2 left-2.5 h-4 w-4 text-muted" />
                  <input aria-label="Filter connections" placeholder="Filter…" className={`${inputCls} w-56 pl-8`} value={term} onChange={(e) => setTerm(e.target.value)} />
                </div>
              }>
                {groups.length === 0 ? (
                  <div className="px-5 pb-5">
                    <Empty title="No client connections">{t ? 'Nothing matches the filter.' : 'No client session in the latest sample.'}</Empty>
                  </div>
                ) : (
                  <div className="overflow-x-auto">
                    <table className={tableCls}>
                      <caption className="sr-only">Connections grouped by database, user, application, client, and state</caption>
                      <thead>
                        <tr>
                          {d.targets.length > 1 && <Th className="pl-5">Instance</Th>}
                          <Th className={d.targets.length > 1 ? undefined : 'pl-5'}>Database</Th>
                          <Th>User</Th>
                          <Th>Application</Th>
                          <Th>Client</Th>
                          <Th>State</Th>
                          <Th align="right">Connections</Th>
                          <Th align="right" className="pr-5">Oldest in state</Th>
                        </tr>
                      </thead>
                      <tbody>
                        {groups.map(({ x, g }, i) => (
                          <tr key={i} className={trCls}>
                            {d.targets.length > 1 && <td className={`${tdCls} pl-5 whitespace-nowrap`}>{x.target.name}</td>}
                            <td className={`${tdCls} ${d.targets.length > 1 ? '' : 'pl-5'}`}>{g.database || '—'}</td>
                            <td className={tdCls}>{g.user || '—'}</td>
                            <td className={tdCls}>{g.application || <span className="text-muted">—</span>}</td>
                            <td className={`${tdCls} font-mono text-xs`}>{g.client_addr || 'local socket'}</td>
                            <td className={tdCls}>
                              <Badge tone={stateTone(g.state || null)}>{g.state || 'unknown'}</Badge>
                            </td>
                            <td className={tdNum}>{num(g.count)}</td>
                            <td className={`${tdNum} pr-5`}>{seconds(g.oldest_seconds)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </Card>
            </div>
          )
        }}
      </Load>
    </>
  )
}
