import { useEffect, useMemo, useRef } from 'react'
import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, MarkAreaComponent, TooltipComponent } from 'echarts/components'
import { SVGRenderer } from 'echarts/renderers'
import { cssVar } from '../lib/commands'
import { time as fmtTime, UNKNOWN } from '../lib/format'
import { useThemeVersion } from '../lib/theme'

echarts.use([LineChart, GridComponent, TooltipComponent, MarkAreaComponent, SVGRenderer])

export interface ChartSeries {
  key: string
  label: string
  /** A CSS color or var(--token). */
  color: string
  /** [epoch ms, value]; null values break the line (unknown, never zero). */
  points: [number, number | null][]
}

export interface GapRange {
  from: number
  to: number
}

/**
 * Time-series chart: lines, or stacked areas for parts of a whole. One y
 * axis only. A legend with each series' latest value sits above the plot
 * when there are two or more series; hovering shows a crosshair tooltip.
 */
export function TimeChart({ series, format, height = 220, stacked = false, from, to, gaps = [], label, emptyText = 'No samples in this time range' }: {
  series: ChartSeries[]
  format: (v: number) => string
  height?: number
  stacked?: boolean
  from: number
  to: number
  gaps?: GapRange[]
  label: string
  emptyText?: string
}) {
  const el = useRef<HTMLDivElement>(null)
  const chart = useRef<echarts.ECharts | null>(null)
  const themeVersion = useThemeVersion()
  const hasData = series.some((s) => s.points.some((p) => p[1] !== null))

  const option = useMemo(() => {
    void themeVersion
    const ink2 = cssVar('var(--ink-2)')
    const muted = cssVar('var(--muted)')
    const grid = cssVar('var(--grid)')
    const axis = cssVar('var(--axis)')
    const surface = cssVar('var(--surface)')
    const crit = cssVar('var(--warn-mark)')
    const span = to - from
    return {
      animation: false,
      grid: { left: 8, right: 12, top: 10, bottom: 4, containLabel: true },
      tooltip: {
        trigger: 'axis',
        axisPointer: { type: 'line', lineStyle: { color: axis } },
        backgroundColor: surface,
        borderColor: axis,
        textStyle: { color: ink2, fontSize: 12 },
        confine: true,
        formatter: (items: { axisValue: number; marker: string; seriesName: string; value: [number, number | null] }[]) => {
          const first = items[0]
          if (!first) return ''
          const head = `<div style="margin-bottom:4px;color:${muted}">${fmtTime(new Date(first.axisValue).toISOString(), { date: span > 86400000 })}</div>`
          return head + items.map((i) => `<div style="display:flex;justify-content:space-between;gap:16px">${i.marker}<span style="flex:1">${i.seriesName}</span><b>${i.value[1] === null || i.value[1] === undefined ? UNKNOWN : format(i.value[1])}</b></div>`).join('')
        },
      },
      xAxis: {
        type: 'time',
        min: from,
        max: to,
        axisLine: { lineStyle: { color: axis } },
        axisTick: { show: false },
        splitLine: { show: false },
        axisLabel: {
          color: muted,
          fontSize: 11,
          hideOverlap: true,
          formatter: (v: number) => fmtTime(new Date(v).toISOString(), { seconds: false, date: span > 86400000 * 2 }),
        },
      },
      yAxis: {
        type: 'value',
        min: 0,
        splitNumber: 4,
        axisLabel: { color: muted, fontSize: 11, formatter: (v: number) => format(v) },
        splitLine: { lineStyle: { color: grid, width: 1 } },
      },
      series: series.map((s, i) => {
        const color = cssVar(s.color)
        return {
          name: s.label,
          type: 'line',
          data: s.points,
          stack: stacked ? 'total' : undefined,
          showSymbol: s.points.length <= 40,
          symbolSize: 6,
          connectNulls: false,
          lineStyle: { width: stacked ? 1 : 2, color: stacked ? surface : color },
          itemStyle: { color },
          areaStyle: stacked ? { color, opacity: 0.9 } : undefined,
          emphasis: { focus: 'series' },
          markArea:
            i === 0 && gaps.length
              ? { silent: true, itemStyle: { color: crit, opacity: 0.1 }, data: gaps.map((g) => [{ xAxis: g.from }, { xAxis: g.to }]) }
              : undefined,
        }
      }),
    }
  }, [series, format, stacked, from, to, gaps, themeVersion])

  useEffect(() => {
    if (!el.current || !hasData) return
    const c = echarts.init(el.current, undefined, { renderer: 'svg' })
    chart.current = c
    const ro = new ResizeObserver(() => c.resize())
    ro.observe(el.current)
    return () => {
      ro.disconnect()
      c.dispose()
      chart.current = null
    }
  }, [hasData])

  useEffect(() => {
    chart.current?.setOption(option, true)
  }, [option, hasData])

  const latest = (s: ChartSeries) => {
    for (let i = s.points.length - 1; i >= 0; i--) {
      const v = s.points[i]?.[1]
      if (v !== null && v !== undefined) return v
    }
    return null
  }

  return (
    <figure aria-label={label} className="m-0">
      {series.length > 1 && (
        <ul aria-label={`${label} legend`} className="mb-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-ink-2">
          {series.map((s) => (
            <li key={s.key} className="flex items-center gap-1.5">
              <span aria-hidden className="inline-block h-2.5 w-2.5 rounded-sm" style={{ background: s.color }} />
              <span>{s.label}</span>
              <span className="num font-medium text-ink">{(() => {
                const v = latest(s)
                return v === null ? UNKNOWN : format(v)
              })()}</span>
            </li>
          ))}
        </ul>
      )}
      {hasData ? (
        <div ref={el} style={{ height }} />
      ) : (
        <div style={{ height }} className="flex items-center justify-center rounded-lg border border-dashed border-line-strong text-sm text-muted">
          {emptyText}
        </div>
      )}
    </figure>
  )
}

/** Converts API buckets to chart points, inserting a null where a bucket is missing so lines break. */
export function toPoints(rows: { t: string; v: number | null }[], stepSeconds: number): [number, number | null][] {
  const out: [number, number | null][] = []
  let prev: number | null = null
  const step = stepSeconds * 1000
  for (const r of rows) {
    const t = Date.parse(r.t)
    if (prev !== null && t - prev > 1.5 * step) out.push([prev + step, null])
    out.push([t, r.v])
    prev = t
  }
  return out
}
