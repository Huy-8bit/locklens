import { useMemo, useState } from 'react'
import { Link } from 'react-router'
import { ArrowRight, X } from 'lucide-react'
import { useLoad } from '../api/hooks'
import type { LoadDimension, LoadGroup, LoadResponse, QueryRef } from '../api/types'
import { num, pct } from '../lib/format'
import { classInfo, LOAD_DIMENSIONS, statementKey } from '../lib/load'
import type { Range } from '../lib/scope'
import { slot } from './MetricCard'
import { SqlText } from './SqlText'
import { TimeChart, toPoints, type ChartSeries, type GapRange } from './TimeChart'
import { Card, cx, ErrorBox, Notice, selectCls, Spinner } from './ui'

const aas = (v: number) => num(v, v < 10 ? 2 : 1)

/** Breakdown of a group's load by wait class, as one thin stacked bar. */
function ClassBar({ classes, total }: { classes: Record<string, number>; total: number }) {
  const parts = Object.entries(classes)
    .filter(([, v]) => v > 0)
    .sort((a, b) => b[1] - a[1])
  const title = parts.map(([k, v]) => `${classInfo(k).label}: ${aas(v)}`).join(' · ')
  return (
    <span className="flex min-w-[8rem] flex-col gap-1" title={title}>
      <span className="flex h-2 w-full overflow-hidden rounded-full bg-surface-3">
        {parts.map(([k, v]) => (
          <span key={k} className="h-full" style={{ width: `${(v / (total || 1)) * 100}%`, background: classInfo(k).color }} />
        ))}
      </span>
      <span className="truncate text-xs text-muted">{parts.slice(0, 2).map(([k, v]) => `${classInfo(k).label} ${pct(v / (total || 1), 0)}`).join(', ')}</span>
    </span>
  )
}

function statementOf(g: LoadGroup, refs: Map<string, QueryRef>) {
  return refs.get(statementKey(g.values))
}

function seriesOf(d: LoadResponse, refs: Map<string, QueryRef>): ChartSeries[] {
  const byClass = d.load.dimension === 'class'
  // The chart is load: sessions idle in a transaction run nothing, so they
  // stay out of the stack (the table and the summary line still show them).
  return d.load.series.filter((s) => !(byClass && s.key === 'IdleTx') && !s.key.startsWith('IdleTx/')).map((s, i) => {
    const g = d.load.top.find((t) => t.key === s.key)
    const ref = g && d.load.dimension === 'query' ? statementOf(g, refs) : undefined
    const text = ref?.query_text?.replace(/\s+/g, ' ').trim()
    return {
      key: s.key,
      label: byClass ? classInfo(s.key).label : text ? (text.length > 48 ? `${text.slice(0, 47)}…` : text) : s.label,
      color: byClass ? classInfo(s.key).color : s.key === '_other' ? 'var(--s-other)' : slot(i),
      points: toPoints(s.points, d.step_seconds),
    }
  })
}

/**
 * Database load from active session sampling: how many sessions were active
 * on average, what they waited on, and which statements, users, or
 * applications they belonged to. Like Grafana or Performance Insights' "load
 * by wait": one stacked chart and the top groups below it.
 */
export function LoadPanel({ targetId, range, title = 'Database load', subtitle, zoomable = true, highlight, initial = 'class', rows = 10 }: {
  targetId: number
  range: Range
  title?: string
  subtitle?: string
  zoomable?: boolean
  highlight?: GapRange
  initial?: LoadDimension
  rows?: number
}) {
  const [by, setBy] = useState<LoadDimension>(initial)
  const [cls, setCls] = useState('')
  const q = useLoad(targetId, range, by, { class: cls || undefined })
  const d = q.data
  const refs = useMemo(() => new Map((d?.statements ?? []).map((r) => [`${r.key.dbid}/${r.key.userid}/${r.key.queryid}`, r])), [d])
  const series = useMemo(() => (d ? seriesOf(d, refs) : []), [d, refs])
  const disabled = d?.collector && !d.collector.enabled ? d.collector.disabled_reason : null
  const interval = d?.collector?.interval_seconds

  return (
    <Card
      title={title}
      subtitle={
        subtitle ??
        `Average active sessions (AAS), sampled from pg_stat_activity${interval ? ` every ${num(interval)} s` : ''}. A value of 4 means four sessions were busy at any moment on average; compare it with the number of CPU cores.`
      }
      actions={
        <div className="flex flex-wrap items-center gap-2">
          {cls && (
            <button type="button" onClick={() => setCls('')} className="inline-flex items-center gap-1 rounded-lg bg-accent-soft px-2 py-1 text-xs font-medium text-accent" aria-label={`Remove the ${classInfo(cls).label} filter`}>
              {classInfo(cls).label} <X aria-hidden className="h-3.5 w-3.5" />
            </button>
          )}
          <label className="flex items-center gap-1.5 text-xs text-muted">
            Group by
            <select className={selectCls} value={by} onChange={(e) => setBy(e.target.value as LoadDimension)}>
              {LOAD_DIMENSIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
          </label>
        </div>
      }
    >
      {q.error ? (
        <ErrorBox error={q.error} title="Could not load database load" />
      ) : !d ? (
        <Spinner label="Loading database load" />
      ) : disabled && d.load.ticks === 0 ? (
        <Notice>Active session sampling is not running for this instance: {disabled}.</Notice>
      ) : (
        <div className="space-y-4">
          <div className="flex flex-wrap gap-x-6 gap-y-1 text-sm">
            <span>
              <span className="text-muted">Active sessions on average </span>
              <strong className="num font-semibold">{d.load.active_aas === null ? '—' : aas(d.load.active_aas)}</strong>
            </span>
            <span>
              <span className="text-muted">Idle in transaction </span>
              <strong className="num font-semibold">{d.load.idle_in_transaction_aas === null ? '—' : aas(d.load.idle_in_transaction_aas)}</strong>
            </span>
            <span className="text-muted">{num(d.load.ticks)} samples</span>
          </div>
          <TimeChart
            label={title}
            series={series}
            format={aas}
            stacked
            from={Date.parse(d.from)}
            to={Date.parse(d.to)}
            height={220}
            zoomable={zoomable}
            highlight={highlight}
            emptyText="No samples in this time range"
          />
          {d.load.top.length > 0 && (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[640px] text-sm">
                <caption className="sr-only">Top {LOAD_DIMENSIONS.find((o) => o.value === by)?.label.toLowerCase()} groups by database load</caption>
                <thead>
                  <tr className="border-b border-line text-left text-xs text-muted">
                    <th className="py-2 pr-3 font-medium">{LOAD_DIMENSIONS.find((o) => o.value === by)?.label}</th>
                    <th className="whitespace-nowrap py-2 pr-3 text-right font-medium">Avg active</th>
                    <th className="w-28 py-2 pr-3 font-medium">Share</th>
                    <th className="py-2 pr-3 font-medium">Waiting on</th>
                    {by === 'class' && <th className="py-2 font-medium"><span className="sr-only">Drill down</span></th>}
                  </tr>
                </thead>
                <tbody>
                  {d.load.top.slice(0, rows).map((g) => {
                    const ref = by === 'query' ? statementOf(g, refs) : undefined
                    const cinfo = by === 'class' ? classInfo(g.key) : by === 'event' ? classInfo(g.key.split('/')[0] ?? '') : null
                    return (
                      <tr key={g.key} className="border-b border-line align-top last:border-0">
                        <td className="max-w-[30rem] py-2 pr-3">
                          {by === 'query' ? (
                            <>
                              {ref ? (
                                <Link to={`/queries/${targetId}/${ref.identity_id}`} className="block hover:underline">
                                  {ref.query_text ? <SqlText sql={ref.query_text} clamp /> : <span className="font-mono text-xs">queryid {g.values[2]}</span>}
                                </Link>
                              ) : (
                                <span className="font-mono text-xs text-ink-2">{g.label}</span>
                              )}
                              <span className="mt-0.5 block text-xs text-muted">
                                {g.values[3] || '?'} · {g.values[4] || '?'}
                                {!ref && g.values[2] !== '0' ? ' · not in pg_stat_statements history yet' : ''}
                              </span>
                            </>
                          ) : (
                            <span className="flex items-center gap-2" title={cinfo?.hint}>
                              {cinfo && <span aria-hidden className="h-2.5 w-2.5 shrink-0 rounded-sm" style={{ background: cinfo.color }} />}
                              <span className={cx(by === 'event' && 'font-mono text-xs')}>{by === 'class' ? cinfo?.label : g.label}</span>
                            </span>
                          )}
                        </td>
                        <td className="num whitespace-nowrap py-2 pr-3 text-right font-medium">{aas(g.aas)}</td>
                        <td className="py-2 pr-3">
                          <span className="flex items-center gap-2">
                            <span className="h-1.5 flex-1 overflow-hidden rounded-full bg-surface-3">
                              <span className="block h-full rounded-full bg-accent-fill" style={{ width: `${Math.round((g.share ?? 0) * 100)}%` }} />
                            </span>
                            <span className="num w-9 text-right text-xs text-ink-2">{pct(g.share, 0)}</span>
                          </span>
                        </td>
                        <td className="py-2 pr-3">{by === 'class' ? <span className="text-xs text-muted">{cinfo?.hint}</span> : <ClassBar classes={g.classes} total={g.aas} />}</td>
                        {by === 'class' && (
                          <td className="py-2 text-right">
                            <button
                              type="button"
                              onClick={() => {
                                setCls(g.key)
                                setBy('query')
                              }}
                              className="inline-flex items-center gap-1 whitespace-nowrap rounded-md px-2 py-1 text-xs font-medium text-accent hover:bg-surface-2"
                            >
                              Statements <ArrowRight aria-hidden className="h-3.5 w-3.5" />
                            </button>
                          </td>
                        )}
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          )}
          {by === 'query' && !d.query_text_enabled && <p className="text-xs text-muted">SQL text is not collected for this instance, so statements show their queryid.</p>}
          {by === 'query' && d.load.top.some((g) => g.values[2] === '0') && (
            <p className="text-xs text-muted">Statements without a query ID: PostgreSQL computes query_id only when compute_query_id is on (or auto with pg_stat_statements loaded).</p>
          )}
        </div>
      )}
    </Card>
  )
}

