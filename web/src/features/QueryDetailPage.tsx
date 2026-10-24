import { useParams, useSearchParams } from 'react-router'
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { useQueryDetail } from '../api/hooks'
import type { QueryDetailResponse } from '../api/types'
import { TimeRangePicker } from '../components/TimeRangePicker'
import { Card, DataState, KeyValue, Notice, PageHeader, Tag } from '../components/ui'
import { fmtMs, fmtNumber, fmtPercent, fmtTime } from '../lib/format'
import { useTimeRange } from '../lib/time'
import { useTargetId } from './TargetOverviewPage'

function BucketChart({ d, field, label, format }: { d: QueryDetailResponse; field: 'calls' | 'mean'; label: string; format: (v: number) => string }) {
  const rows = d.buckets.map((b) => ({ t: Date.parse(b.t), v: field === 'calls' ? b.calls : b.calls > 0 ? b.total_exec_ms / b.calls : null }))
  return (
    <figure aria-label={label} className="m-0 h-[200px]">
      {rows.length === 0 ? (
        <div className="flex h-full items-center justify-center rounded-md border border-dashed border-border text-sm text-muted">No samples in this time range</div>
      ) : (
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={rows}>
            <CartesianGrid stroke="var(--ll-grid)" vertical={false} />
            <XAxis dataKey="t" type="number" scale="time" domain={[Date.parse(d.from), Date.parse(d.to)]} tickFormatter={(t: number) => fmtTime(new Date(t).toISOString(), { seconds: false, date: false })} fontSize={11} stroke="var(--ll-muted)" />
            <YAxis tickFormatter={format} fontSize={11} stroke="var(--ll-muted)" width={72} />
            <Tooltip
              isAnimationActive={false}
              contentStyle={{ background: 'var(--ll-panel)', border: '1px solid var(--ll-border)', fontSize: 12 }}
              labelFormatter={(t) => fmtTime(new Date(Number(t)).toISOString())}
              formatter={(v) => [typeof v === 'number' ? format(v) : 'unknown', label]}
            />
            <Bar dataKey="v" fill="var(--ll-chart-1)" isAnimationActive={false} />
          </BarChart>
        </ResponsiveContainer>
      )}
    </figure>
  )
}

export function QueryDetailPage() {
  const id = useTargetId()
  const qid = Number(useParams().qid)
  const [range, setRange] = useTimeRange('1h')
  const [params, setParams] = useSearchParams()
  const includeText = params.get('text') === 'true'
  const q = useQueryDetail(id, qid, range.params, includeText)
  return (
    <DataState query={q} loadingLabel="Loading query">
      {(d) => {
        const c = d.comparison
        const covCur = d.current.coverage.window_seconds ? d.current.coverage.covered_seconds / d.current.coverage.window_seconds : null
        const covPrev = d.previous.coverage.window_seconds ? d.previous.coverage.covered_seconds / d.previous.coverage.window_seconds : null
        return (
          <>
            <PageHeader
              title={`Query ${d.identity.queryid}`}
              subtitle={`${d.identity.datname} · ${d.identity.username} · ${d.identity.toplevel ? 'top-level' : 'nested'} · PostgreSQL ${d.identity.server_major} · first seen ${fmtTime(d.first_seen)}`}
              actions={<TimeRangePicker range={range} onChange={setRange} />}
            />
            <Card title="Normalised text">
              {!d.query_text_enabled ? (
                <p className="text-sm text-muted">Query text collection is off for this target (default). Enable it per target under Targets & settings if your policy allows; redaction cannot remove every sensitive literal.</p>
              ) : !includeText ? (
                <button type="button" className="text-sm text-accent underline" onClick={() => setParams((p) => { const n = new URLSearchParams(p); n.set('text', 'true'); return n }, { replace: true })}>
                  Show normalised query text
                </button>
              ) : (
                <pre className="overflow-x-auto whitespace-pre-wrap break-all rounded-md bg-panel2 p-3 font-mono text-xs">{d.identity.query_text ?? 'Text not stored for this identity.'}</pre>
              )}
            </Card>
            <div className="mt-4 grid gap-4 lg:grid-cols-2">
              <Card title="Window comparison (equal lengths)">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="text-xs text-muted">
                      <th className="text-left font-medium">Measure</th>
                      <th className="text-right font-medium">Previous window</th>
                      <th className="text-right font-medium">Current window</th>
                      <th className="text-right font-medium">Ratio</th>
                    </tr>
                  </thead>
                  <tbody className="tabular">
                    <tr className="border-t border-border"><td>Calls</td><td className="text-right">{fmtNumber(d.previous.totals.calls)}</td><td className="text-right">{fmtNumber(d.current.totals.calls)}</td><td className="text-right">{c.calls_ratio?.toFixed(2) ?? '—'}</td></tr>
                    <tr className="border-t border-border"><td>Mean time per call</td><td className="text-right">{fmtMs(d.previous.mean_exec_ms, 'no calls')}</td><td className="text-right">{fmtMs(d.current.mean_exec_ms, 'no calls')}</td><td className="text-right">{c.mean_ratio?.toFixed(2) ?? '—'}</td></tr>
                    <tr className="border-t border-border"><td>Total time</td><td className="text-right">{fmtMs(d.previous.totals.total_exec_ms)}</td><td className="text-right">{fmtMs(d.current.totals.total_exec_ms)}</td><td className="text-right">{c.total_time_ratio?.toFixed(2) ?? '—'}</td></tr>
                    <tr className="border-t border-border"><td>Rows</td><td className="text-right">{fmtNumber(d.previous.totals.rows)}</td><td className="text-right">{fmtNumber(d.current.totals.rows)}</td><td /></tr>
                    <tr className="border-t border-border"><td>Window covered</td><td className="text-right">{fmtPercent(covPrev)}</td><td className="text-right">{fmtPercent(covCur)}</td><td /></tr>
                  </tbody>
                </table>
                <div className="mt-3 flex flex-wrap gap-2 text-xs">
                  {c.too_few_calls && <Tag>too few calls to classify</Tag>}
                  {c.mean_regression && <Tag tone="crit">slower per call</Tag>}
                  {c.volume_increase && <Tag tone="warn">more calls</Tag>}
                  {c.load_increase && <Tag tone="warn">more total execution time</Tag>}
                  {!c.too_few_calls && !c.mean_regression && !c.volume_increase && !c.load_increase && <Tag>no significant change</Tag>}
                </div>
                {((covCur ?? 0) < 0.8 || (covPrev ?? 0) < 0.8) && (
                  <div className="mt-3">
                    <Notice tone="warn">A window is less than 80% covered by collections (recent start, gaps, or resets). Compare with care.</Notice>
                  </div>
                )}
              </Card>
              <Card title="Identity">
                <KeyValue
                  items={[
                    ['queryid', <code key="q">{d.identity.queryid}</code>],
                    ['Database', d.identity.datname],
                    ['User', d.identity.username],
                    ['Top level', d.identity.toplevel ? 'yes' : 'no (nested statement)'],
                    ['Server major version', String(d.identity.server_major)],
                    ['First / last seen', `${fmtTime(d.first_seen)} / ${fmtTime(d.last_seen)}`],
                  ]}
                />
                <p className="mt-3 text-xs text-muted">pg_stat_statements is a set of cumulative counters per normalised statement, not a log of every execution. Entries can be evicted (pg_stat_statements.max) and counters reset; queryid is not stable across major versions.</p>
              </Card>
              <Card title="Calls per bucket">
                <BucketChart d={d} field="calls" label="calls" format={(v) => fmtNumber(v)} />
              </Card>
              <Card title="Mean execution time per bucket">
                <BucketChart d={d} field="mean" label="mean time" format={(v) => fmtMs(v)} />
              </Card>
            </div>
          </>
        )
      }}
    </DataState>
  )
}
