import { useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { useQueries, useTarget } from '../api/hooks'
import type { QueryItem } from '../api/types'
import { TimeRangePicker } from '../components/TimeRangePicker'
import { Card, DataState, Empty, Notice, PageHeader, Pagination, Tag, cx } from '../components/ui'
import { fmtBytes, fmtMs, fmtNumber, fmtPercent, fmtRate } from '../lib/format'
import { useTimeRange } from '../lib/time'
import { useTargetId } from './TargetOverviewPage'

const COLUMNS: { key: string; label: string; sort?: string; align?: 'right' }[] = [
  { key: 'query', label: 'Query' },
  { key: 'calls', label: 'Calls', sort: 'calls', align: 'right' },
  { key: 'cps', label: 'Calls/s', align: 'right' },
  { key: 'total', label: 'Total time', sort: 'total_time', align: 'right' },
  { key: 'share', label: 'Share', align: 'right' },
  { key: 'mean', label: 'Mean (window)', sort: 'mean_time', align: 'right' },
  { key: 'rows', label: 'Rows', sort: 'rows', align: 'right' },
  { key: 'read', label: 'Blocks read', sort: 'shared_read', align: 'right' },
  { key: 'temp', label: 'Temp blocks', sort: 'temp_written', align: 'right' },
  { key: 'wal', label: 'WAL', sort: 'wal_bytes', align: 'right' },
]

function Flags({ q }: { q: QueryItem }) {
  const c = q.comparison
  if (!c) return null
  if (c.too_few_calls) return <Tag>too few calls to compare</Tag>
  return (
    <span className="flex flex-wrap gap-1">
      {c.mean_regression && <Tag tone="crit">slower per call ×{c.mean_ratio?.toFixed(1)}</Tag>}
      {c.volume_increase && <Tag tone="warn">more calls ×{c.calls_ratio?.toFixed(1)}</Tag>}
      {c.load_increase && !c.mean_regression && !c.volume_increase && <Tag tone="warn">more total time ×{c.total_time_ratio?.toFixed(1)}</Tag>}
    </span>
  )
}

export function QueriesPage() {
  const id = useTargetId()
  const target = useTarget(id)
  const [range, setRange] = useTimeRange('1h')
  const [params, setParams] = useSearchParams()
  const [filterDraft, setFilterDraft] = useState(params.get('q') ?? '')
  const sort = params.get('sort') ?? 'total_time'
  const order = params.get('order') === 'asc' ? 'asc' : 'desc'
  const page = Math.max(1, Number(params.get('page') ?? '1') || 1)
  const compare = params.get('compare') !== 'false'
  const includeText = params.get('text') === 'true'
  const includeMonitoring = params.get('monitoring') === 'true'
  const set = (patch: Record<string, string | null>) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        for (const [k, v] of Object.entries(patch)) {
          if (v === null) next.delete(k)
          else next.set(k, v)
        }
        return next
      },
      { replace: true },
    )
  const q = useQueries(id, { range: range.params, sort, order, q: params.get('q') ?? '', page, compare, includeText, includeMonitoring })
  const textAllowed = target.data?.query_text_enabled ?? false

  return (
    <>
      <PageHeader
        title="Query workload"
        subtitle="Aggregated pg_stat_statements deltas. Means are per window (total time ÷ calls); percentiles are not available from this source."
        actions={<TimeRangePicker range={range} onChange={setRange} />}
      />
      <Card>
        <form
          className="mb-3 flex flex-wrap items-end gap-3"
          onSubmit={(e) => {
            e.preventDefault()
            set({ q: filterDraft || null, page: null })
          }}
        >
          <label className="text-sm">
            <span className="mb-1 block text-muted">Filter (database, user, queryid{includeText ? ', text' : ''})</span>
            <input className="w-64 rounded-md border border-border bg-bg px-2 py-1" value={filterDraft} onChange={(e) => setFilterDraft(e.target.value)} />
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={compare} onChange={(e) => set({ compare: e.target.checked ? null : 'false', page: null })} />
            Compare with the previous window of equal length
          </label>
          <label className={cx('flex items-center gap-2 text-sm', !textAllowed && 'text-muted')} title={textAllowed ? '' : 'Query text collection is off for this target (Targets & settings)'}>
            <input type="checkbox" disabled={!textAllowed} checked={includeText && textAllowed} onChange={(e) => set({ text: e.target.checked ? 'true' : null })} />
            Show normalised query text
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={includeMonitoring} onChange={(e) => set({ monitoring: e.target.checked ? 'true' : null, page: null })} />
            Include LockLens' own monitoring queries{target.data ? ` (user ${target.data.user})` : ''}
          </label>
          <button type="submit" className="rounded-md border border-border px-3 py-1 text-sm hover:bg-panel2">
            Apply filter
          </button>
        </form>
        <DataState query={q} loadingLabel="Loading workload">
          {(d) => {
            const cov = d.coverage.window_seconds > 0 ? d.coverage.covered_seconds / d.coverage.window_seconds : 0
            return (
              <>
                {d.available === false && <Notice tone="warn">pg_stat_statements is unavailable for this target{d.unavailable_reason ? `: ${d.unavailable_reason}` : ''}.</Notice>}
                <div className="mb-2 flex flex-wrap gap-2 text-xs">
                  <Tag tone={cov < 0.8 ? 'warn' : 'muted'}>window covered by collections: {fmtPercent(cov)}</Tag>
                  {d.previous_coverage && <Tag>previous window covered: {fmtPercent(d.previous_coverage.window_seconds ? d.previous_coverage.covered_seconds / d.previous_coverage.window_seconds : null)}</Tag>}
                  {d.coverage.resets > 0 && <Tag tone="warn">{d.coverage.resets} statistics reset(s)</Tag>}
                  {d.coverage.evicted_entries > 0 && <Tag tone="warn">{d.coverage.evicted_entries} evicted entries (activity missing)</Tag>}
                  {d.coverage.hidden_entries_max > 0 && <Tag tone="warn">up to {d.coverage.hidden_entries_max} entries hidden by permissions</Tag>}
                  <Tag>flags need ≥ {d.min_calls} calls in both windows</Tag>
                  {d.monitoring_hidden && <Tag>statements of {d.monitoring_user} hidden</Tag>}
                </div>
                {d.items.length === 0 ? (
                  <Empty title="No statement activity in this window">Collections run about once a minute; the first one after start is a baseline without deltas.</Empty>
                ) : (
                  <div className="overflow-x-auto">
                    <table className="w-full min-w-[960px] text-sm">
                      <caption className="sr-only">Statements by {sort}</caption>
                      <thead>
                        <tr className="border-b border-border text-xs text-muted">
                          {COLUMNS.map((c) => (
                            <th key={c.key} scope="col" className={cx('py-2 pr-3 font-medium', c.align === 'right' ? 'text-right' : 'text-left')} aria-sort={c.sort === sort ? (order === 'asc' ? 'ascending' : 'descending') : undefined}>
                              {c.sort ? (
                                <button type="button" className="hover:underline" onClick={() => set({ sort: c.sort ?? null, order: c.sort === sort && order === 'desc' ? 'asc' : 'desc', page: null })}>
                                  {c.label}
                                  {c.sort === sort ? (order === 'asc' ? ' ↑' : ' ↓') : ''}
                                </button>
                              ) : (
                                c.label
                              )}
                            </th>
                          ))}
                        </tr>
                      </thead>
                      <tbody>
                        {d.items.map((r) => (
                          <tr key={r.identity_id} className="border-b border-border align-top">
                            <td className="max-w-[28rem] py-2 pr-3">
                              <Link className="font-mono text-accent hover:underline" to={`/targets/${id}/queries/${r.identity_id}?${new URLSearchParams(range.params)}`}>
                                {r.queryid}
                              </Link>
                              <div className="text-xs text-muted">
                                {r.datname} · {r.username}
                                {!r.toplevel && ' · nested'}
                              </div>
                              {r.query_text && <code className="mt-1 line-clamp-2 block break-all text-xs">{r.query_text}</code>}
                              <div className="mt-1">
                                <Flags q={r} />
                              </div>
                            </td>
                            <td className="py-2 pr-3 text-right tabular">{fmtNumber(r.calls)}</td>
                            <td className="py-2 pr-3 text-right tabular">{fmtRate(r.calls_per_second, '')}</td>
                            <td className="py-2 pr-3 text-right tabular">{fmtMs(r.total_exec_ms)}</td>
                            <td className="py-2 pr-3 text-right tabular">{fmtPercent(r.share_of_total_time)}</td>
                            <td className="py-2 pr-3 text-right tabular">
                              {fmtMs(r.mean_exec_ms)}
                              {compare && <div className="text-xs text-muted">before {fmtMs(r.previous_mean_exec_ms, 'no calls')}</div>}
                            </td>
                            <td className="py-2 pr-3 text-right tabular">{fmtNumber(r.rows)}</td>
                            <td className="py-2 pr-3 text-right tabular">{fmtNumber(r.shared_blks_read)}</td>
                            <td className="py-2 pr-3 text-right tabular">{fmtNumber(r.temp_blks_written)}</td>
                            <td className="py-2 pr-3 text-right tabular">{fmtBytes(r.wal_bytes)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
                <Pagination page={page} pageSize={d.page_size} total={d.total} onPage={(p) => set({ page: String(p) })} />
              </>
            )
          }}
        </DataState>
      </Card>
    </>
  )
}
