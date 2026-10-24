import { useMemo } from 'react'
import { CartesianGrid, Legend, Line, LineChart, ReferenceArea, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import type { Gap, Series } from '../api/types'
import { fmtTime, UNKNOWN } from '../lib/format'

const COLORS = ['var(--ll-chart-1)', 'var(--ll-chart-2)', 'var(--ll-chart-3)', 'var(--ll-chart-4)', 'var(--ll-chart-5)']

export interface ChartSeries {
  key: string
  label: string
  series?: Series
}

type Row = { t: number } & Record<string, number | null>

/**
 * Line chart for downsampled series. A bucket whose samples were all unknown
 * and a missing bucket (no collection) both break the line; they are never
 * drawn as zero. Recorded collection gaps are shaded.
 */
export function TimeSeriesChart({ series, stepSeconds, format, gaps = [], height = 200, from, to, ariaLabel, integer = false }: {
  series: ChartSeries[]
  stepSeconds: number
  format: (v: number) => string
  gaps?: Gap[]
  height?: number
  from: string
  to: string
  ariaLabel: string
  /** Counts: axis ticks at whole numbers (bucket averages may still be fractional). */
  integer?: boolean
}) {
  const data = useMemo(() => {
    const rows = new Map<number, Row>()
    const stepMs = stepSeconds * 1000
    for (const s of series) {
      const pts = s.series?.points ?? []
      let prev: number | null = null
      for (const p of pts) {
        const t = Date.parse(p.t)
        if (prev !== null && t - prev > 1.5 * stepMs) {
          // Insert an explicit break inside a period without samples.
          const gapT = prev + stepMs
          const r = rows.get(gapT) ?? { t: gapT }
          r[s.key] = null
          rows.set(gapT, r)
        }
        const r = rows.get(t) ?? { t }
        r[s.key] = p.v
        rows.set(t, r)
        prev = t
      }
    }
    return [...rows.values()].sort((a, b) => a.t - b.t)
  }, [series, stepSeconds])
  const hasData = data.some((r) => series.some((s) => typeof r[s.key] === 'number'))
  const domain: [number, number] = [Date.parse(from), Date.parse(to)]
  return (
    <figure aria-label={ariaLabel} className="m-0" style={{ height }}>
      {!hasData ? (
        <div className="flex h-full items-center justify-center rounded-md border border-dashed border-border text-sm text-muted">
          No samples in this time range
        </div>
      ) : (
        <ResponsiveContainer width="100%" height="100%">
          <LineChart data={data} margin={{ top: 6, right: 12, bottom: 0, left: 4 }}>
            <CartesianGrid stroke="var(--ll-grid)" vertical={false} />
            <XAxis
              dataKey="t"
              type="number"
              scale="time"
              domain={domain}
              tickFormatter={(t: number) => fmtTime(new Date(t).toISOString(), { seconds: false, date: domain[1] - domain[0] > 86400000 })}
              stroke="var(--ll-muted)"
              fontSize={11}
              minTickGap={40}
            />
            <YAxis tickFormatter={(v: number) => format(v)} stroke="var(--ll-muted)" fontSize={11} width={72} allowDecimals={!integer} />
            <Tooltip
              isAnimationActive={false}
              contentStyle={{ background: 'var(--ll-panel)', border: '1px solid var(--ll-border)', borderRadius: 6, fontSize: 12 }}
              labelFormatter={(t) => fmtTime(new Date(Number(t)).toISOString())}
              formatter={(v, name) => [typeof v === 'number' ? format(v) : UNKNOWN, String(name)]}
            />
            {series.length > 1 && <Legend wrapperStyle={{ fontSize: 12 }} />}
            {gaps.map((g, i) => (
              <ReferenceArea key={i} x1={Date.parse(g.from)} x2={Date.parse(g.to)} fill="var(--ll-warn)" fillOpacity={0.12} ifOverflow="hidden" />
            ))}
            {series.map((s, i) => (
              <Line
                key={s.key}
                type="monotone"
                dataKey={s.key}
                name={s.label}
                stroke={COLORS[i % COLORS.length]}
                strokeWidth={1.75}
                dot={data.length <= 60 ? { r: 2 } : false}
                connectNulls={false}
                isAnimationActive={false}
              />
            ))}
          </LineChart>
        </ResponsiveContainer>
      )}
    </figure>
  )
}
