import { useMemo } from 'react'
import { Link, useParams } from 'react-router'
import { ArrowLeft } from 'lucide-react'
import { useQueryDetail } from '../api/hooks'
import type { QueryDetailResponse } from '../api/types'
import { slot } from '../components/MetricCard'
import { PageHeader, useNumParam } from '../components/Shell'
import { CopyButton, formatSql, SqlText } from '../components/SqlText'
import { TimeChart } from '../components/TimeChart'
import { Badge, Card, KeyValue, Load, Notice, Stat } from '../components/ui'
import { commandColor, COMMAND_LABEL } from '../lib/commands'
import { compact, ms, num, rate, time } from '../lib/format'
import { useRange } from '../lib/scope'

function bucketSeries(d: QueryDetailResponse) {
  const step = d.step_seconds * 1000
  const calls: [number, number | null][] = []
  const mean: [number, number | null][] = []
  let prev: number | null = null
  for (const b of d.buckets) {
    const t = Date.parse(b.t)
    if (prev !== null && t - prev > 1.5 * step) {
      calls.push([prev + step, null])
      mean.push([prev + step, null])
    }
    const known = !b.gap && b.covered_seconds > 0
    calls.push([t, known ? b.calls / b.covered_seconds : null])
    mean.push([t, known && b.calls > 0 ? b.total_exec_ms / b.calls : null])
    prev = t
  }
  return { calls, mean }
}

export default function QueryDetailPage() {
  const id = useNumParam('targetId')
  const qid = Number(useParams().qid)
  const [range] = useRange()
  const q = useQueryDetail(id, qid, range)
  const series = useMemo(() => (q.data ? bucketSeries(q.data) : null), [q.data])

  return (
    <>
      <Link to={`/databases/${id}/queries`} className="mb-2 inline-flex items-center gap-1 text-sm text-accent hover:underline">
        <ArrowLeft aria-hidden className="h-4 w-4" /> All queries
      </Link>
      <Load q={q} label="Loading statement">
        {(d) => {
          const it = d.identity
          const cur = d.current
          const prev = d.previous
          const from = Date.parse(d.from)
          const to = Date.parse(d.to)
          return (
            <>
              <PageHeader
                title={
                  <span className="inline-flex items-center gap-2">
                    <span aria-hidden className="h-3 w-3 rounded-sm" style={{ background: commandColor(it.command) }} />
                    {COMMAND_LABEL[it.command] ?? it.command} statement
                  </span>
                }
                subtitle={`${it.datname} · ${it.username} · queryid ${it.queryid}`}
              />
              <Card title="SQL" actions={it.query_text ? <CopyButton text={it.query_text} label="Copy SQL" /> : undefined}>
                {it.query_text ? (
                  <div className="max-h-96 overflow-auto rounded-lg bg-surface-2 p-3">
                    <SqlText sql={formatSql(it.query_text)} />
                  </div>
                ) : (
                  <Notice>SQL text is not collected for this instance (enable it in Settings).</Notice>
                )}
              </Card>
              <div className="mt-3 grid grid-cols-2 gap-3 md:grid-cols-4">
                <Stat label="Calls in range" value={compact(cur.totals.calls)} sub={cur.coverage.covered_seconds > 0 ? `${rate(cur.totals.calls / cur.coverage.covered_seconds)} on average` : undefined} />
                <Stat label="Average time" value={ms(cur.mean_exec_ms)} sub={`before: ${ms(prev.mean_exec_ms)}`} tone={d.comparison.mean_regression ? 'warn' : undefined} />
                <Stat label="Total time" value={ms(cur.totals.total_exec_ms)} sub={`before: ${ms(prev.totals.total_exec_ms)}`} />
                <Stat label="Rows per call" value={cur.totals.calls ? num(cur.totals.rows / cur.totals.calls, 1) : '—'} sub={`${compact(cur.totals.rows)} rows in total`} />
              </div>
              {(d.comparison.mean_regression || d.comparison.volume_increase) && (
                <div className="mt-3 flex flex-wrap gap-2">
                  {d.comparison.mean_regression && <Badge tone="warn">Each call is {d.comparison.mean_ratio?.toFixed(1)}× slower than in the previous period</Badge>}
                  {d.comparison.volume_increase && <Badge tone="accent">Runs {d.comparison.calls_ratio?.toFixed(1)}× more often than before</Badge>}
                </div>
              )}
              {series && (
                <div className="mt-3 grid gap-3 lg:grid-cols-2">
                  <Card title="Calls per second">
                    <TimeChart label="Calls per second" series={[{ key: 'calls', label: 'Calls/s', color: slot(0), points: series.calls }]} format={(n) => rate(n)} from={from} to={to} height={200} />
                  </Card>
                  <Card title="Average time per call">
                    <TimeChart label="Average time per call" series={[{ key: 'mean', label: 'Avg time', color: slot(1), points: series.mean }]} format={(n) => ms(n)} from={from} to={to} height={200} />
                  </Card>
                </div>
              )}
              <Card className="mt-3" title="Details">
                <KeyValue
                  items={[
                    ['Database', it.datname],
                    ['User', it.username],
                    ['Top-level statement', it.toplevel ? 'yes' : 'no (called from a function)'],
                    ['First seen', time(d.first_seen, { date: true })],
                    ['Last seen', time(d.last_seen, { date: true })],
                    ['Previous period', `${time(prev.from, { date: true })} – ${time(prev.to, { date: true })}`],
                  ]}
                />
                <p className="mt-3 text-xs text-muted">Averages compare equal-length windows. A slower average does not identify the cause: plans, lock waits, I/O, and larger results all count. LockLens never runs EXPLAIN automatically.</p>
              </Card>
            </>
          )
        }}
      </Load>
    </>
  )
}
