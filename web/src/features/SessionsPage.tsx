import { useActivity } from '../api/hooks'
import type { Report } from '../api/types'
import { Card, DataState, Notice, PageHeader, Tag } from '../components/ui'
import { fmtDuration, fmtNumber, fmtTime } from '../lib/format'
import { MetricChart, useTargetId } from './TargetOverviewPage'
import { TimeRangePicker } from '../components/TimeRangePicker'
import { useTimeRange } from '../lib/time'

function SessionTable({ report }: { report: Report }) {
  const ages = new Map(report.analysis.sessions.map((a) => [a.pid, a]))
  const nodes = new Map(report.analysis.nodes.map((n) => [n.id, n]))
  const rows = [...report.snapshot.sessions].sort((a, b) => (ages.get(b.pid)?.transaction_age_seconds ?? -1) - (ages.get(a.pid)?.transaction_age_seconds ?? -1))
  if (rows.length === 0) return <p className="text-sm text-muted">No sessions with an open transaction or involved in blocking.</p>
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[900px] text-sm">
        <caption className="sr-only">Sessions with open transactions or involved in blocking</caption>
        <thead>
          <tr className="border-b border-border text-left text-xs text-muted">
            <th className="py-2 pr-3 font-medium">PID</th>
            <th className="py-2 pr-3 font-medium">State</th>
            <th className="py-2 pr-3 font-medium">Transaction age</th>
            <th className="py-2 pr-3 font-medium">Query age</th>
            <th className="py-2 pr-3 font-medium">Lock wait</th>
            <th className="py-2 pr-3 font-medium">Wait event</th>
            <th className="py-2 pr-3 font-medium">Blocks</th>
            <th className="py-2 pr-3 font-medium">User · app</th>
            <th className="py-2 font-medium">Client</th>
          </tr>
        </thead>
        <tbody className="tabular">
          {rows.map((s) => {
            const a = ages.get(s.pid)
            const n = nodes.get(s.pid)
            return (
              <tr key={s.pid} className="border-b border-border">
                <td className="py-1.5 pr-3 font-mono">{s.pid}</td>
                <td className="py-1.5 pr-3">
                  {s.state ?? <Tag tone="warn">hidden</Tag>}
                  {s.state?.startsWith('idle in transaction') && <span className="ml-1 text-warn">●</span>}
                </td>
                <td className="py-1.5 pr-3">{fmtDuration(a?.transaction_age_seconds)}</td>
                <td className="py-1.5 pr-3">{fmtDuration(a?.query_age_seconds)}</td>
                <td className="py-1.5 pr-3">{n?.blocked_by.length ? fmtDuration(a?.lock_wait_seconds) : '—'}</td>
                <td className="py-1.5 pr-3">{s.wait_event_type ? `${s.wait_event_type}:${s.wait_event ?? ''}` : '—'}</td>
                <td className="py-1.5 pr-3">{n?.direct_blocked_count ? `${n.direct_blocked_count} / ${n.transitive_blocked_count}` : '—'}</td>
                <td className="max-w-[16rem] truncate py-1.5 pr-3">
                  {s.user ?? 'unknown'} · {s.application_name || '—'}
                </td>
                <td className="py-1.5">{s.client_addr ?? '—'}</td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

export function SessionsPage() {
  const id = useTargetId()
  const q = useActivity(id)
  const [range, setRange] = useTimeRange('1h')
  return (
    <>
      <PageHeader title="Sessions and long transactions" subtitle="Transaction age, query age, and lock wait age are different measurements." actions={<TimeRangePicker range={range} onChange={setRange} />} />
      <DataState query={q} loadingLabel="Loading sessions">
        {(d) =>
          !d.report ? (
            <p className="text-sm text-muted">No activity collected yet.</p>
          ) : (
            <div className="space-y-4">
              {d.counts && (
                <div className="flex flex-wrap gap-2 text-xs">
                  <Tag>observed {fmtTime(d.observed_at)}</Tag>
                  <Tag>{fmtNumber(d.counts.client_total)} client sessions</Tag>
                  <Tag>{fmtNumber(d.counts.active)} active</Tag>
                  <Tag>{fmtNumber(d.counts.idle)} idle</Tag>
                  <Tag tone={d.counts.idle_in_transaction ? 'warn' : 'muted'}>{fmtNumber(d.counts.idle_in_transaction + d.counts.idle_in_transaction_aborted)} idle in transaction</Tag>
                  {d.counts.unknown_state > 0 && <Tag tone="warn">{d.counts.unknown_state} with hidden state</Tag>}
                </div>
              )}
              {d.report.snapshot.warnings.map((w) => (
                <Notice key={w.code}>{w.message}</Notice>
              ))}
              <Card title={`Long-running transactions (≥ ${fmtDuration(d.report.analysis.min_tx_age_seconds)})`}>
                {d.report.analysis.long_transactions.length === 0 ? (
                  <p className="text-sm text-muted">None at the current threshold.</p>
                ) : (
                  <ul className="space-y-1 text-sm">
                    {d.report.analysis.long_transactions.map((lt) => (
                      <li key={lt.pid} className="flex flex-wrap gap-3">
                        <span className="font-mono">pid {lt.pid}</span>
                        <span className={lt.idle_in_transaction ? 'text-warn' : ''}>{lt.state ?? 'unknown'}</span>
                        <span>open for {fmtDuration(lt.transaction_age_seconds)}</span>
                        {lt.direct_blocked_count > 0 && <Tag tone="crit">blocks {lt.direct_blocked_count}</Tag>}
                      </li>
                    ))}
                  </ul>
                )}
              </Card>
              <Card title="Sessions with open transactions or involved in blocking">
                <SessionTable report={d.report} />
              </Card>
              <MetricChart id={id} title="Open transactions" metrics={['transactions.open', 'transactions.long', 'sessions.idle_in_transaction']} labels={['open', 'long-running', 'idle in transaction']} format={(n) => fmtNumber(n, 1)} range={range} />
            </div>
          )
        }
      </DataState>
    </>
  )
}
