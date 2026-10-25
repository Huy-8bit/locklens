import { useEffect, useMemo, useRef, useState, type MouseEvent as ReactMouseEvent } from 'react'
import * as echarts from 'echarts/core'
import { BarChart, LineChart } from 'echarts/charts'
import { GridComponent, MarkAreaComponent, TooltipComponent } from 'echarts/components'
import { SVGRenderer } from 'echarts/renderers'
import { Maximize2, X } from 'lucide-react'
import { cssVar } from '../lib/commands'
import { time as fmtTime, UNKNOWN } from '../lib/format'
import { absolute, MIN_WINDOW_MS, useScope, zoomedOut } from '../lib/scope'
import { useThemeVersion } from '../lib/theme'
import { cx } from './ui'

echarts.use([LineChart, BarChart, GridComponent, TooltipComponent, MarkAreaComponent, SVGRenderer])

// Every time chart joins one group, so hovering one shows the crosshair and
// values at the same moment on all charts of the page.
const GROUP = 'locklens-time'
let connected = false

export interface ChartSeries {
  key: string
  label: string
  /** A CSS color or var(--token). */
  color: string
  /** [epoch ms, value]; null values break the line (unknown, never zero). */
  points: [number, number | null][]
  /** Draw as bars instead of a line (for example query throughput). */
  bars?: boolean
}

export interface GapRange {
  from: number
  to: number
}

function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c] ?? c)
}

interface TimeChartProps {
  series: ChartSeries[]
  format: (v: number) => string
  height?: number
  stacked?: boolean
  from: number
  to: number
  gaps?: GapRange[]
  label: string
  emptyText?: string
  /** Lines with a light fill below them. */
  area?: boolean
  /** Show the legend row (series names and latest values). */
  legend?: boolean
  /** Hide x-axis labels (for a pane stacked above another chart). */
  compactAxis?: boolean
  /** Fixed y-axis label width in px, so stacked panes line up on the x axis. */
  axisWidth?: number
  /** Whole-number y-axis steps (counts, bytes). */
  integer?: boolean
  /** Offer the "view larger" button. */
  expandable?: boolean
}

/**
 * Time-series chart: lines, or stacked areas for parts of a whole. One y
 * axis only. Like a Grafana panel: drag across the plot to zoom every chart
 * of the page into that window, double-click to zoom out, click a legend
 * entry to show only that series (Ctrl/⌘/Shift-click to toggle it), and
 * open a larger view. Hovering shows a crosshair synchronised across charts.
 */
export function TimeChart(props: TimeChartProps) {
  const { series, format, height = 220, stacked = false, area = false, from, to, gaps = [], label, emptyText = 'No samples in this time range', legend = true, compactAxis = false, axisWidth, integer = false, expandable = true } = props
  const el = useRef<HTMLDivElement>(null)
  const chart = useRef<echarts.ECharts | null>(null)
  const themeVersion = useThemeVersion()
  const { range, setRange } = useScope()
  const [hidden, setHidden] = useState<Set<string>>(() => new Set())
  const [selection, setSelection] = useState<{ a: number; b: number } | null>(null)
  const [expanded, setExpanded] = useState(false)
  const visible = useMemo(() => series.filter((s) => !hidden.has(s.key)), [series, hidden])
  const hasData = series.some((s) => s.points.some((p) => p[1] !== null))

  const option = useMemo(() => {
    void themeVersion
    const muted = cssVar('var(--muted)')
    const grid = cssVar('var(--grid)')
    const axis = cssVar('var(--axis)')
    const surface = cssVar('var(--surface)')
    const crit = cssVar('var(--warn-mark)')
    const span = to - from
    const colors = new Map(series.map((s) => [s.label, cssVar(s.color)]))
    return {
      animation: false,
      grid: axisWidth ? { left: axisWidth, right: 12, top: 10, bottom: compactAxis ? 8 : 24 } : { left: 8, right: 12, top: 10, bottom: compactAxis ? 0 : 4, containLabel: true },
      tooltip: {
        trigger: 'axis',
        className: 'll-tooltip',
        axisPointer: { type: 'line', lineStyle: { color: axis } },
        backgroundColor: surface,
        borderColor: axis,
        confine: true,
        // Plain HTML with classes only: the page's Content Security Policy
        // refuses inline style attributes (see .ll-tt-* in styles.css).
        formatter: (items: { axisValue: number; seriesName: string; value: [number, number | null] }[]) => {
          const first = items[0]
          if (!first) return ''
          const rows = [...items].sort((a, b) => (b.value[1] ?? -Infinity) - (a.value[1] ?? -Infinity))
          const total = rows.reduce((n, i) => n + (i.value[1] ?? 0), 0)
          const known = rows.some((i) => i.value[1] !== null && i.value[1] !== undefined)
          const row = (name: string, color: string | null, value: string, cls = '') =>
            `<div class="ll-tt-row ${cls}">${color ? `<svg class="ll-tt-dot" width="10" height="10" viewBox="0 0 10 10" aria-hidden="true"><rect width="10" height="10" rx="2" fill="${escapeHtml(color)}"/></svg>` : '<span class="ll-tt-dot"></span>'}<span class="ll-tt-name">${escapeHtml(name)}</span><span class="ll-tt-val">${escapeHtml(value)}</span></div>`
          return (
            `<div class="ll-tt-time">${escapeHtml(fmtTime(new Date(first.axisValue).toISOString(), { date: span > 86_400_000 }))}</div>` +
            rows.map((i) => row(i.seriesName, colors.get(i.seriesName) ?? null, i.value[1] === null || i.value[1] === undefined ? UNKNOWN : format(i.value[1]))).join('') +
            (stacked && rows.length > 1 && known ? row('Total', null, format(total), 'll-tt-total') : '')
          )
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
          show: !compactAxis,
          color: muted,
          fontSize: 11,
          hideOverlap: true,
          formatter: (v: number) => fmtTime(new Date(v).toISOString(), { seconds: span < 900_000, date: span > 86_400_000 * 2 }),
        },
      },
      yAxis: {
        type: 'value',
        min: 0,
        minInterval: integer ? 1 : undefined,
        splitNumber: 4,
        axisLabel: { color: muted, fontSize: 11, formatter: (v: number) => format(v) },
        splitLine: { lineStyle: { color: grid, width: 1 } },
      },
      series: visible.map((s, i) => {
        const color = cssVar(s.color)
        if (s.bars) {
          return { name: s.label, type: 'bar', data: s.points, barMaxWidth: 8, itemStyle: { color, borderRadius: [2, 2, 0, 0] }, emphasis: { focus: 'series' } }
        }
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
          areaStyle: stacked ? { color, opacity: 0.9 } : area ? { color, opacity: 0.18 } : undefined,
          emphasis: { focus: 'series' },
          markArea: i === 0 && gaps.length ? { silent: true, itemStyle: { color: crit, opacity: 0.1 }, data: gaps.map((g) => [{ xAxis: g.from }, { xAxis: g.to }]) } : undefined,
        }
      }),
    }
  }, [series, visible, format, stacked, area, compactAxis, axisWidth, integer, from, to, gaps, themeVersion])

  useEffect(() => {
    if (!el.current || !hasData) return
    const c = echarts.init(el.current, undefined, { renderer: 'svg' })
    c.group = GROUP
    if (!connected) {
      echarts.connect(GROUP)
      connected = true
    }
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

  // Drag to zoom, double-click to zoom out.
  useEffect(() => {
    const c = chart.current
    if (!c || !hasData) return
    const zr = c.getZr()
    let start: number | null = null
    const inPlot = (x: number, y: number) => c.containPixel({ gridIndex: 0 }, [x, y])
    const toTime = (x: number) => Number(c.convertFromPixel({ xAxisIndex: 0 }, x))
    const down = (e: { offsetX: number; offsetY: number; event?: { button?: number } }) => {
      if ((e.event?.button ?? 0) !== 0 || !inPlot(e.offsetX, e.offsetY)) return
      start = e.offsetX
      setSelection({ a: e.offsetX, b: e.offsetX })
    }
    const move = (e: { offsetX: number }) => {
      if (start !== null) setSelection({ a: start, b: e.offsetX })
    }
    const up = (e: { offsetX: number }) => {
      const s = start
      start = null
      setSelection(null)
      if (s === null || Math.abs(e.offsetX - s) < 6) return
      let a = Math.max(from, Math.min(toTime(s), toTime(e.offsetX)))
      let b = Math.min(to, Math.max(toTime(s), toTime(e.offsetX)))
      if (!Number.isFinite(a) || !Number.isFinite(b)) return
      if (b - a < MIN_WINDOW_MS) {
        const mid = (a + b) / 2
        a = mid - MIN_WINDOW_MS / 2
        b = mid + MIN_WINDOW_MS / 2
      }
      setRange(absolute(a, b), true)
    }
    const cancel = () => {
      start = null
      setSelection(null)
    }
    const dbl = () => setRange(zoomedOut(range), true)
    zr.on('mousedown', down)
    zr.on('mousemove', move)
    zr.on('mouseup', up)
    zr.on('globalout', cancel)
    zr.on('dblclick', dbl)
    return () => {
      zr.off('mousedown', down)
      zr.off('mousemove', move)
      zr.off('mouseup', up)
      zr.off('globalout', cancel)
      zr.off('dblclick', dbl)
    }
  }, [hasData, from, to, range, setRange])

  const latest = (s: ChartSeries) => {
    for (let i = s.points.length - 1; i >= 0; i--) {
      const v = s.points[i]?.[1]
      if (v !== null && v !== undefined) return v
    }
    return null
  }

  // Grafana's legend: click shows only that series (click again to show
  // all); Ctrl/⌘/Shift-click toggles one series.
  const onLegend = (key: string, e: ReactMouseEvent) => {
    setHidden((h) => {
      if (e.metaKey || e.ctrlKey || e.shiftKey) {
        const n = new Set(h)
        if (n.has(key)) n.delete(key)
        else n.add(key)
        return n
      }
      const onlyThis = series.every((s) => (s.key === key ? !h.has(s.key) : h.has(s.key)))
      return onlyThis ? new Set() : new Set(series.filter((s) => s.key !== key).map((s) => s.key))
    })
  }

  return (
    <figure aria-label={label} className="group relative m-0">
      {legend && series.length > 1 && (
        <ul aria-label={`${label} legend`} className="mb-2 flex flex-wrap gap-x-1 gap-y-1 pr-8 text-xs text-ink-2">
          {series.map((s) => {
            const on = !hidden.has(s.key)
            return (
              <li key={s.key}>
                <button
                  type="button"
                  aria-pressed={on}
                  title="Click: show only this series · Ctrl/⌘-click: toggle it"
                  onClick={(e) => onLegend(s.key, e)}
                  className={cx('flex items-center gap-1.5 rounded-md px-1.5 py-0.5 hover:bg-surface-2', !on && 'opacity-45')}
                >
                  <span aria-hidden className="inline-block h-2.5 w-2.5 rounded-sm" style={{ background: s.color }} />
                  <span className={cx(!on && 'line-through')}>{s.label}</span>
                  <span className="num font-medium text-ink">
                    {(() => {
                      const v = latest(s)
                      return v === null ? UNKNOWN : format(v)
                    })()}
                  </span>
                </button>
              </li>
            )
          })}
        </ul>
      )}
      {hasData ? (
        <div className="relative" title="Drag to zoom · double-click to zoom out">
          <div ref={el} style={{ height }} className="cursor-crosshair select-none" />
          {selection && Math.abs(selection.b - selection.a) > 2 && (
            <div aria-hidden className="pointer-events-none absolute top-0 bottom-0 border-x border-accent bg-accent/15" style={{ left: Math.min(selection.a, selection.b), width: Math.abs(selection.b - selection.a) }} />
          )}
        </div>
      ) : (
        <div style={{ height }} className="flex items-center justify-center rounded-lg border border-dashed border-line-strong text-sm text-muted">
          {emptyText}
        </div>
      )}
      {expandable && hasData && (
        <button
          type="button"
          onClick={() => setExpanded(true)}
          aria-label={`View ${label} larger`}
          title="View larger"
          className="absolute top-0 right-0 rounded-md p-1 text-muted opacity-0 transition-opacity group-hover:opacity-100 hover:bg-surface-2 hover:text-ink focus:opacity-100"
        >
          <Maximize2 aria-hidden className="h-4 w-4" />
        </button>
      )}
      {expanded && <ChartModal label={label} onClose={() => setExpanded(false)} chart={props} />}
    </figure>
  )
}

/** A large view of one chart (Grafana's "View panel"); Escape closes it. */
function ChartModal({ label, chart, onClose }: { label: string; chart: TimeChartProps; onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null
    ref.current?.focus()
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('keydown', onKey)
      prev?.focus()
    }
  }, [onClose])
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" onClick={onClose}>
      <div ref={ref} tabIndex={-1} role="dialog" aria-modal="true" aria-label={label} onClick={(e) => e.stopPropagation()} className="w-full max-w-6xl rounded-2xl border border-line bg-surface p-5 shadow-2xl outline-none">
        <div className="mb-3 flex items-center justify-between gap-3">
          <h2 className="text-base font-semibold text-ink">{label}</h2>
          <div className="flex items-center gap-3">
            <span className="hidden text-xs text-muted sm:inline">Drag to zoom · double-click to zoom out</span>
            <button type="button" onClick={onClose} aria-label="Close" className="rounded-md p-1.5 text-ink-2 hover:bg-surface-2">
              <X aria-hidden className="h-4 w-4" />
            </button>
          </div>
        </div>
        <TimeChart {...chart} height={Math.max(320, Math.round(window.innerHeight * 0.6))} expandable={false} legend />
      </div>
    </div>
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
