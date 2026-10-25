import { useMemo, type ReactNode } from 'react'
import { useMetrics } from '../api/hooks'
import type { MetricsResponse } from '../api/types'
import type { Range } from '../lib/time'
import { Card, ErrorBox, Spinner } from './ui'
import { TimeChart, toPoints, type ChartSeries } from './TimeChart'

export interface MetricDef {
  metric: string
  label: string
  color: string
}

const SLOTS = ['var(--s1)', 'var(--s2)', 'var(--s3)', 'var(--s4)', 'var(--s5)', 'var(--s6)', 'var(--s7)']

/** Series colors in the validated categorical order. */
export function slot(i: number): string {
  return SLOTS[i % SLOTS.length] ?? 'var(--s1)'
}

export function seriesFrom(data: MetricsResponse, defs: MetricDef[]): ChartSeries[] {
  return defs.map((d) => {
    const s = data.series.find((x) => x.metric === d.metric && !x.label)
    return { key: d.metric, label: d.label, color: d.color, points: s ? toPoints(s.points.map((p) => ({ t: p.t, v: p.gap ? null : p.v })), data.step_seconds) : [] }
  })
}

/** A card with a historical time-series chart of one or more metrics. */
export function MetricCard({ id, title, subtitle, defs, format, range, stacked, height, actions }: {
  id: number
  title: string
  subtitle?: ReactNode
  defs: MetricDef[]
  format: (v: number) => string
  range: Range
  stacked?: boolean
  height?: number
  actions?: ReactNode
}) {
  const q = useMetrics(id, defs.map((d) => d.metric), range)
  const series = useMemo(() => (q.data ? seriesFrom(q.data, defs) : []), [q.data, defs])
  return (
    <Card title={title} subtitle={subtitle} actions={actions}>
      {q.data ? (
        <TimeChart
          label={title}
          series={series}
          format={format}
          stacked={stacked}
          height={height}
          from={Date.parse(q.data.from)}
          to={Date.parse(q.data.to)}
          gaps={q.data.gaps.map((g) => ({ from: Date.parse(g.from), to: Date.parse(g.to) }))}
        />
      ) : q.error ? (
        <ErrorBox error={q.error} title="Could not load the chart" />
      ) : (
        <div style={{ height: (height ?? 220) + 24 }}>
          <Spinner label="Loading chart" />
        </div>
      )}
    </Card>
  )
}
