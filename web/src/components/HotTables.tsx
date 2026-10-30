import { useMemo, useState } from 'react'
import { Link } from 'react-router'
import { useMetrics, useTables } from '../api/hooks'
import type { MetricsResponse } from '../api/types'
import { compact, num, rate } from '../lib/format'
import { useRange } from '../lib/scope'
import { slot } from './MetricCard'
import { tableCls, tdCls, tdNum, Th, trCls } from './Table'
import { TimeChart, toPoints, type ChartSeries } from './TimeChart'
import { Card, Empty, Segmented } from './ui'

type Measure = 'table.seq_rows.per_s' | 'table.writes.per_s' | 'table.idx_scans.per_s'
const MEASURES: { value: Measure; label: string }[] = [
  { value: 'table.seq_rows.per_s', label: 'Rows read sequentially' },
  { value: 'table.writes.per_s', label: 'Rows written' },
  { value: 'table.idx_scans.per_s', label: 'Index scans' },
]

/** One series per table (label) of a per-table metric, the busiest five. */
function perTable(data: MetricsResponse, metric: string): ChartSeries[] {
  const series = data.series.filter((s) => s.metric === metric && s.label)
  const total = (pts: { v: number | null }[]) => pts.reduce((n, p) => n + (p.v ?? 0), 0)
  return series
    .sort((a, b) => total(b.points) - total(a.points))
    .slice(0, 5)
    .map((s, i) => ({ key: s.label ?? '', label: s.label ?? '', color: slot(i), points: toPoints(s.points.map((p) => ({ t: p.t, v: p.gap ? null : p.v })), data.step_seconds) }))
}

/** The busiest tables of the monitored databases: the last minute, and over time. */
export function HotTables({ targetId }: { targetId: number }) {
  const [range] = useRange()
  const [measure, setMeasure] = useState<Measure>('table.seq_rows.per_s')
  const live = useTables(targetId)
  const history = useMetrics(targetId, [measure], range)
  const series = useMemo(() => (history.data ? perTable(history.data, measure) : []), [history.data, measure])
  const rows = live.data?.activity ?? []
  // Data browsing works on the connected database only.
  const connected = live.data?.tables?.datname
  const several = new Set(rows.map((r) => r.datname)).size > 1
  return (
    <Card
      title="Hot tables"
      subtitle="The busiest tables of the monitored databases, from pg_stat_user_tables every minute (no locks taken). Many rows read sequentially on a large table often means a missing index."
    >
      <div className="space-y-4">
        {rows.length === 0 ? (
          <Empty title="No table activity yet">Rates need two collections, about a minute apart.</Empty>
        ) : (
          <div className="overflow-x-auto">
            <table className={tableCls}>
              <caption className="sr-only">Busiest tables in the last minute</caption>
              <thead>
                <tr>
                  <Th>Table</Th>
                  <Th align="right">Seq scans/s</Th>
                  <Th align="right">Rows read seq./s</Th>
                  <Th align="right">Index scans/s</Th>
                  <Th align="right">Rows written/s</Th>
                  <Th align="right">Dead rows</Th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => (
                  <tr key={`${r.datname}.${r.relid}`} className={trCls}>
                    <td className={tdCls}>
                      {several && <span className="mr-1 font-mono text-xs text-muted">{r.datname}.</span>}
                      {!r.datname || r.datname === connected ? (
                        <Link to={`/explorer/${targetId}/${encodeURIComponent(r.schema)}/${encodeURIComponent(r.name)}`} className="font-mono text-xs text-accent hover:underline">
                          {r.schema}.{r.name}
                        </Link>
                      ) : (
                        <span className="font-mono text-xs">
                          {r.schema}.{r.name}
                        </span>
                      )}
                    </td>
                    <td className={tdNum}>{num(r.seq_scans_per_s, 2)}</td>
                    <td className={tdNum}>{rate(r.seq_rows_per_s)}</td>
                    <td className={tdNum}>{num(r.idx_scans_per_s, 2)}</td>
                    <td className={tdNum}>{rate(r.writes_per_s)}</td>
                    <td className={tdNum}>{compact(r.dead_rows)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <Segmented label="Hot tables chart" value={measure} onChange={setMeasure} items={MEASURES} />
        {history.data && (
          <TimeChart
            label={`${MEASURES.find((m) => m.value === measure)?.label ?? measure} per table`}
            series={series}
            format={(v) => (measure === 'table.idx_scans.per_s' ? num(v, 2) : rate(v))}
            from={Date.parse(history.data.from)}
            to={Date.parse(history.data.to)}
            height={200}
          />
        )}
      </div>
    </Card>
  )
}
