import { useMemo, useState } from 'react'
import type { CommandsResponse } from '../api/types'
import { CHART_GROUPS, commandColor, commandGroup, COMMAND_LABEL, groupLabel } from '../lib/commands'
import { compact, ms, num, pct, rate } from '../lib/format'
import { TimeChart, type ChartSeries } from './TimeChart'
import { Segmented } from './ui'

export type CommandMetric = 'calls' | 'time' | 'rows'

const METRIC: Record<CommandMetric, { label: string; pick: (b: { calls: number; total_exec_ms: number; rows: number }) => number; format: (v: number) => string }> = {
  calls: { label: 'Queries/s', pick: (b) => b.calls, format: (v) => rate(v) },
  time: { label: 'Execution time', pick: (b) => b.total_exec_ms, format: (v) => `${ms(v)}/s` },
  rows: { label: 'Rows/s', pick: (b) => b.rows, format: (v) => rate(v) },
}

/**
 * Per-second rates of each chart group per bucket. A bucket with collection
 * coverage but no statements of a group is zero; a bucket without coverage
 * is unknown (null), and a missing bucket breaks the line.
 */
export function commandSeries(data: CommandsResponse, metric: CommandMetric): ChartSeries[] {
  const m = METRIC[metric]
  const step = data.step_seconds * 1000
  const groups = CHART_GROUPS.filter((g) => data.buckets.some((b) => commandGroup(b.command) === g))
  const byTime = new Map<string, typeof data.buckets>()
  for (const b of data.buckets) byTime.set(b.t, [...(byTime.get(b.t) ?? []), b])
  const cov = [...data.coverage_buckets].sort((a, b) => a.t.localeCompare(b.t))
  return groups.map((g) => {
    const points: [number, number | null][] = []
    let prev: number | null = null
    for (const c of cov) {
      const ts = Date.parse(c.t)
      if (prev !== null && ts - prev > 1.5 * step) points.push([prev + step, null])
      const sum = (byTime.get(c.t) ?? []).filter((b) => commandGroup(b.command) === g).reduce((n, b) => n + m.pick(b), 0)
      points.push([ts, c.covered_seconds > 0 ? sum / c.covered_seconds : null])
      prev = ts
    }
    return { key: g, label: groupLabel(g), color: commandColor(g), points }
  })
}

/**
 * Stacked rates per statement class over time. A bucket's sum is divided by
 * the collection time it covers, so partially covered buckets are not
 * understated; uncovered buckets stay empty.
 */
export function CommandsChart({ data, height = 240, metric: fixed }: { data: CommandsResponse; height?: number; metric?: CommandMetric }) {
  const [chosen, setMetric] = useState<CommandMetric>('calls')
  const metric = fixed ?? chosen
  const m = METRIC[metric]
  const series = useMemo(() => commandSeries(data, metric), [data, metric])
  return (
    <div>
      {!fixed && (
        <div className="mb-2">
          <Segmented label="Measure" value={metric} onChange={setMetric} items={[{ value: 'calls', label: 'Queries/s' }, { value: 'time', label: 'Execution time' }, { value: 'rows', label: 'Rows/s' }]} />
        </div>
      )}
      <TimeChart label={`${m.label} by statement type`} series={series} format={m.format} stacked height={height} from={Date.parse(data.from)} to={Date.parse(data.to)} emptyText="No statements were collected in this time range" />
    </div>
  )
}

/** The window's workload per statement class: a share bar plus a table. */
export function CommandMix({ data, onPick, active, compactView = false }: { data: CommandsResponse; onPick?: (c: string) => void; active?: string; compactView?: boolean }) {
  const totals = data.totals
  const calls = totals.reduce((n, t) => n + t.calls, 0)
  const time = totals.reduce((n, t) => n + t.total_exec_ms, 0)
  const covered = data.coverage.covered_seconds
  if (totals.length === 0) {
    return <p className="py-4 text-sm text-muted">No statements were collected in this time range.</p>
  }
  return (
    <div>
      <div className="mb-1 flex justify-between text-xs text-muted">
        <span>Share of executions</span>
        <span className="num">{compact(calls)} total</span>
      </div>
      <div className="flex h-3 w-full gap-[2px] overflow-hidden rounded" role="img" aria-label="Share of executions by statement type">
        {totals.map((t) => (
          <div key={t.command} title={`${COMMAND_LABEL[t.command] ?? t.command}: ${pct(calls ? t.calls / calls : null)}`} style={{ width: `${calls ? (t.calls / calls) * 100 : 0}%`, background: commandColor(t.command) }} />
        ))}
      </div>
      <div className="mt-3 overflow-x-auto">
      <table className="w-full text-sm">
        <caption className="sr-only">Workload by statement type</caption>
        <thead>
          <tr className="text-xs text-muted">
            <th scope="col" className="py-1 text-left font-medium">Type</th>
            <th scope="col" className="whitespace-nowrap py-1 pl-3 text-right font-medium">Executions</th>
            {!compactView && <th scope="col" className="whitespace-nowrap py-1 pl-3 text-right font-medium">Avg per second</th>}
            <th scope="col" className="whitespace-nowrap py-1 pl-3 text-right font-medium">Avg time</th>
            {!compactView && <th scope="col" className="whitespace-nowrap py-1 pl-3 text-right font-medium">Share of time</th>}
            {!compactView && <th scope="col" className="whitespace-nowrap py-1 pl-3 text-right font-medium">Rows</th>}
          </tr>
        </thead>
        <tbody>
          {totals.map((t) => {
            const label = COMMAND_LABEL[t.command] ?? t.command
            return (
              <tr key={t.command} className={active === t.command ? 'bg-accent-soft' : 'border-t border-line'}>
                <td className="py-1.5 pr-2">
                  {onPick ? (
                    <button type="button" onClick={() => onPick(active === t.command ? '' : t.command)} className="inline-flex items-center gap-2 text-left hover:underline" aria-pressed={active === t.command}>
                      <span aria-hidden className="h-2.5 w-2.5 shrink-0 rounded-sm" style={{ background: commandColor(t.command) }} />
                      {label}
                    </button>
                  ) : (
                    <span className="inline-flex items-center gap-2">
                      <span aria-hidden className="h-2.5 w-2.5 shrink-0 rounded-sm" style={{ background: commandColor(t.command) }} />
                      {label}
                    </span>
                  )}
                  <span className="mt-0.5 block whitespace-nowrap pl-[18px] text-xs text-muted">{num(t.statements)} statement{t.statements === 1 ? '' : 's'}</span>
                </td>
                <td className="num whitespace-nowrap py-1.5 pl-3 text-right">
                  {compact(t.calls)} <span className="text-xs text-muted">{pct(calls ? t.calls / calls : null, 0)}</span>
                </td>
                {!compactView && <td className="num whitespace-nowrap py-1.5 pl-3 text-right">{covered > 0 ? rate(t.calls / covered) : '—'}</td>}
                <td className="num whitespace-nowrap py-1.5 pl-3 text-right">{ms(t.mean_exec_ms)}</td>
                {!compactView && <td className="num whitespace-nowrap py-1.5 pl-3 text-right">{pct(time ? t.total_exec_ms / time : null, 0)}</td>}
                {!compactView && <td className="num whitespace-nowrap py-1.5 pl-3 text-right">{compact(t.rows)}</td>}
              </tr>
            )
          })}
        </tbody>
      </table>
      </div>
    </div>
  )
}
