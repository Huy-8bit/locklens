import { useMemo, useState } from 'react'
import { Search } from 'lucide-react'
import { useActivity } from '../api/hooks'
import { BlockingTree } from '../components/BlockingTree'
import { HotTables } from '../components/HotTables'
import { SectionHeader, useNumParam } from '../components/Shell'
import { SessionDetail, stateTone } from '../components/widgets/SessionsTable'
import { SqlText } from '../components/SqlText'
import { tableCls, tdCls, tdNum, Th, trCls } from '../components/Table'
import { Badge, Card, Drawer, Empty, inputCls, Load, Notice, Segmented, Stat } from '../components/ui'
import { ago, ageOf, num, seconds, time } from '../lib/format'

type Filter = 'all' | 'active' | 'idle_tx' | 'waiting'

export default function ActivityPage() {
  const id = useNumParam('id')
  const q = useActivity(id)
  const [filter, setFilter] = useState<Filter>('all')
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState<number | null>(null)

  const report = q.data?.report ?? null
  const rows = useMemo(() => {
    if (!report) return []
    const now = report.snapshot.server_time
    const blocked = new Set(report.analysis.nodes.filter((n) => n.blocked_by.length).map((n) => n.id))
    const term = search.trim().toLowerCase()
    return report.snapshot.sessions
      .filter((s) => s.backend_type === 'client backend' || s.backend_type === null)
      .filter((s) => (filter === 'active' ? s.state === 'active' : filter === 'idle_tx' ? s.state?.startsWith('idle in transaction') : filter === 'waiting' ? blocked.has(s.pid) || s.wait_event_type === 'Lock' : true))
      .filter((s) => !term || [s.query, s.user, s.application_name, s.database, String(s.pid), s.client_addr].some((x) => x?.toLowerCase().includes(term)))
      .map((s) => ({ s, queryAge: s.state === 'active' ? ageOf(s.query_start, now) : null, txAge: ageOf(s.xact_start, now), blocked: blocked.has(s.pid) }))
      .sort((a, b) => (b.txAge ?? -1) - (a.txAge ?? -1))
  }, [report, filter, search])

  const sel = report?.snapshot.sessions.find((s) => s.pid === selected)

  return (
    <>
      <SectionHeader title="Live activity" subtitle="Sessions and running queries from the latest collection. Refreshes after every sample." />
      <Load q={q} label="Loading sessions">
        {(d) => {
          if (!d.report) return <Empty title="No activity collected yet">The first sample arrives within one collection interval after the instance is reachable.</Empty>
          const c = d.counts
          const sum = d.report.analysis.summary
          return (
            <div className="space-y-3">
              <p className="text-xs text-muted">
                Observed {ago(d.observed_at)} ({time(d.observed_at)}){d.source === 'stored' ? ' · from the store: the instance is not connected right now' : ''}
              </p>
              <div className="grid grid-cols-2 gap-3 md:grid-cols-5">
                <Stat label="Client sessions" value={num(c?.client_total)} sub={c ? `${num(c.used_connections)} of ${num(c.max_connections)} connections used` : undefined} />
                <Stat label="Running queries" value={num(c?.active)} />
                <Stat label="Idle in transaction" value={num(c ? c.idle_in_transaction + c.idle_in_transaction_aborted : null)} tone={c && c.idle_in_transaction > 0 ? 'warn' : undefined} sub="holding a transaction open" />
                <Stat label="Waiting for a lock" value={num(c?.waiting_lock)} tone={c && c.waiting_lock > 0 ? 'crit' : undefined} />
                <Stat label="Blocked sessions" value={num(sum.blocked_sessions)} tone={sum.blocked_sessions > 0 ? 'crit' : undefined} sub={`${num(sum.root_blockers)} root blocker(s)`} />
              </div>
              {d.report.snapshot.warnings.some((w) => w.code === 'restricted_visibility') && <Notice tone="warn">Some sessions are hidden from the monitoring role (grant pg_read_all_stats for full detail).</Notice>}
              {d.report.analysis.components.length > 0 && (
                <Card title="Who is blocking whom" subtitle="From pg_blocking_pids(): each root blocker and the sessions waiting behind it">
                  <BlockingTree report={d.report} onSelect={setSelected} />
                </Card>
              )}
              <Card pad={false} title="Sessions" subtitle="Client sessions that are running a query, hold a transaction open, or take part in blocking. Plain idle connections are counted above but not listed.">
                <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 pb-3">
                  <Segmented label="Session state" value={filter} onChange={setFilter} items={[{ value: 'all', label: 'All' }, { value: 'active', label: 'Running' }, { value: 'idle_tx', label: 'Idle in transaction' }, { value: 'waiting', label: 'Waiting / blocked' }]} />
                  <div className="relative ml-auto">
                    <Search aria-hidden className="pointer-events-none absolute top-2 left-2.5 h-4 w-4 text-muted" />
                    <input aria-label="Search sessions" placeholder="Search query, user, app, pid…" className={`${inputCls} w-64 pl-8`} value={search} onChange={(e) => setSearch(e.target.value)} />
                  </div>
                </div>
                {rows.length === 0 ? (
                  <div className="p-4">
                    <Empty title="No sessions match">{filter === 'all' && !search ? 'No client session is busy right now.' : 'Try another filter.'}</Empty>
                  </div>
                ) : (
                  <div className="overflow-x-auto">
                    <table className={tableCls}>
                      <caption className="sr-only">Sessions</caption>
                      <thead>
                        <tr>
                          <Th>PID</Th>
                          <Th>State</Th>
                          <Th align="right">Running for</Th>
                          <Th align="right">Transaction age</Th>
                          <Th>Waiting on</Th>
                          <Th>User · application</Th>
                          <Th className="w-[40%]">Query</Th>
                        </tr>
                      </thead>
                      <tbody>
                        {rows.map(({ s, queryAge, txAge, blocked }) => (
                          <tr key={s.pid} className={`${trCls} cursor-pointer`} onClick={() => setSelected(s.pid)}>
                            <td className={tdCls}>
                              <button type="button" className="font-mono hover:underline" onClick={() => setSelected(s.pid)}>
                                {s.pid}
                              </button>
                            </td>
                            <td className={tdCls}>
                              <Badge tone={blocked ? 'crit' : stateTone(s.state)}>{blocked ? 'blocked' : s.state ?? 'hidden'}</Badge>
                            </td>
                            <td className={tdNum}>{seconds(queryAge)}</td>
                            <td className={tdNum}>{txAge === null ? '—' : seconds(txAge)}</td>
                            <td className={`${tdCls} text-xs text-ink-2`}>{s.wait_event ? `${s.wait_event_type}: ${s.wait_event}` : '—'}</td>
                            <td className={`${tdCls} text-xs`}>
                              <div>{s.user ?? '—'}</div>
                              <div className="text-muted">{s.application_name || s.client_addr || '—'}</div>
                            </td>
                            <td className={tdCls}>{s.query ? <SqlText sql={s.query} clamp /> : <span className="text-xs text-muted">{s.details_visible ? '—' : 'hidden'}</span>}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </Card>
              <HotTables targetId={id} />
            </div>
          )
        }}
      </Load>
      <Drawer open={!!sel && !!report} title={sel ? `Session ${sel.pid}` : ''} onClose={() => setSelected(null)}>
        {sel && report && <SessionDetail s={sel} report={report} />}
      </Drawer>
    </>
  )
}
