import { useEffect, useRef, useState, type FormEvent } from 'react'
import { CalendarDays, ChevronDown, ChevronLeft, ChevronRight, Undo2, ZoomOut } from 'lucide-react'
import { absolute, isAbsolute, MAX_WINDOW_MS, MIN_WINDOW_MS, rangeLabel, RANGES, rangeWindow, shifted, useScope, zoomedOut } from '../lib/scope'
import { cx } from './ui'

/** A datetime-local value (local time, minutes) for an epoch. */
function local(ms: number): string {
  const d = new Date(ms)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`
}

const iconBtn = 'flex h-9 w-8 items-center justify-center text-ink-2 hover:bg-surface-2 hover:text-ink disabled:cursor-not-allowed disabled:opacity-40'

/**
 * Grafana-style time controls: shift back, zoom out, shift forward, back to
 * the previous range, and a panel with quick ranges and an absolute window.
 * Every chart and table of the page follows this range; dragging across a
 * chart zooms into a window.
 */
export function TimeRangePicker({ compact = false }: { compact?: boolean }) {
  const { range, setRange, history, back } = useScope()
  const [open, setOpen] = useState(false)
  const [fromText, setFromText] = useState('')
  const [toText, setToText] = useState('')
  const [error, setError] = useState('')
  const ref = useRef<HTMLDivElement>(null)
  // The current time, refreshed every 15 s, so rendering stays pure (it
  // decides whether "move forward" can go anywhere).
  const [now, setNow] = useState(0)

  useEffect(() => {
    const tick = () => setNow(Date.now())
    const first = window.setTimeout(tick, 0)
    const id = window.setInterval(tick, 15_000)
    return () => {
      window.clearTimeout(first)
      window.clearInterval(id)
    }
  }, [])

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => ref.current && !ref.current.contains(e.target as Node) && setOpen(false)
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false)
    document.addEventListener('mousedown', onDown)
    window.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      window.removeEventListener('keydown', onKey)
    }
  }, [open])

  // Opening the panel fills the absolute fields with the current window.
  const toggle = () => {
    if (!open) {
      const t0 = Date.now()
      const [f, t] = rangeWindow(range, t0)
      setFromText(local(f))
      setToText(local(t))
      setError('')
      setNow(t0)
    }
    setOpen((o) => !o)
  }

  const apply = (e: FormEvent) => {
    e.preventDefault()
    const f = Date.parse(fromText)
    const t = Date.parse(toText)
    if (!Number.isFinite(f) || !Number.isFinite(t)) return setError('Enter both times.')
    if (t - f < MIN_WINDOW_MS) return setError('"To" must be at least a minute after "From".')
    if (t - f > MAX_WINDOW_MS) return setError('At most 31 days.')
    if (f > Date.now()) return setError('"From" is in the future.')
    setRange(absolute(f, Math.min(t, Date.now())), true)
    setOpen(false)
  }

  const atNow = !isAbsolute(range) || now - rangeWindow(range, now)[1] < 5000

  return (
    <div ref={ref} className="relative flex items-center">
      <div className="flex items-center overflow-hidden rounded-xl border border-line bg-surface">
        {!compact && (
          <button type="button" className={iconBtn} onClick={() => setRange(shifted(range, -1), true)} aria-label="Move the time range back" title="Move back">
            <ChevronLeft aria-hidden className="h-4 w-4" />
          </button>
        )}
        <button
          type="button"
          aria-haspopup="dialog"
          aria-expanded={open}
          onClick={toggle}
          className={cx('flex h-9 items-center gap-2 px-3 text-sm font-medium text-ink hover:bg-surface-2', !compact && 'border-x border-line')}
        >
          <CalendarDays aria-hidden className="h-4 w-4 text-ink-2" />
          <span className="sr-only">Time range: </span>
          <span className={cx('num whitespace-nowrap', compact && 'max-w-[11rem] truncate')}>{rangeLabel(range)}</span>
          <ChevronDown aria-hidden className="h-4 w-4 text-muted" />
        </button>
        {!compact && (
          <>
            <button type="button" className={iconBtn} onClick={() => setRange(shifted(range, 1), true)} disabled={atNow} aria-label="Move the time range forward" title="Move forward">
              <ChevronRight aria-hidden className="h-4 w-4" />
            </button>
            <button type="button" className={cx(iconBtn, 'border-l border-line')} onClick={() => setRange(zoomedOut(range), true)} aria-label="Zoom out" title="Zoom out (or double-click a chart)">
              <ZoomOut aria-hidden className="h-4 w-4" />
            </button>
          </>
        )}
      </div>
      {history.length > 0 && (
        <button type="button" onClick={back} className="ml-1.5 flex h-9 items-center gap-1 rounded-xl px-2 text-xs font-medium text-accent hover:bg-surface-2" title="Back to the previous range">
          <Undo2 aria-hidden className="h-4 w-4" />
          {!compact && 'Back'}
        </button>
      )}
      {open && (
        <div role="dialog" aria-label="Choose a time range" className={cx('absolute top-11 z-40 w-[22rem] max-w-[calc(100vw-1.5rem)] rounded-2xl border border-line bg-surface p-4 shadow-xl', compact ? 'left-0' : 'right-0')}>
          <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.15fr)] gap-4">
            <div>
              <p className="mb-1.5 text-xs font-semibold tracking-wide text-muted uppercase">Quick ranges</p>
              <ul className="space-y-0.5">
                {RANGES.map((r) => (
                  <li key={r.value}>
                    <button
                      type="button"
                      onClick={() => {
                        setRange(r.value)
                        setOpen(false)
                      }}
                      className={cx('w-full rounded-lg px-2 py-1.5 text-left text-sm hover:bg-surface-2', range === r.value ? 'bg-accent-soft font-medium text-accent' : 'text-ink')}
                    >
                      {r.label}
                    </button>
                  </li>
                ))}
              </ul>
            </div>
            <form onSubmit={apply} className="space-y-2">
              <p className="mb-1.5 text-xs font-semibold tracking-wide text-muted uppercase">Absolute range</p>
              <label className="block text-xs text-ink-2">
                From
                <input type="datetime-local" required value={fromText} max={local(now)} onChange={(e) => setFromText(e.target.value)} className="mt-0.5 block w-full rounded-lg border border-line-strong bg-surface px-2 py-1.5 text-sm text-ink" />
              </label>
              <label className="block text-xs text-ink-2">
                To
                <input type="datetime-local" required value={toText} max={local(now)} onChange={(e) => setToText(e.target.value)} className="mt-0.5 block w-full rounded-lg border border-line-strong bg-surface px-2 py-1.5 text-sm text-ink" />
              </label>
              {error && <p className="text-xs text-crit">{error}</p>}
              <button type="submit" className="w-full rounded-lg bg-accent-fill px-3 py-1.5 text-sm font-semibold text-accent-ink hover:opacity-95">
                Apply
              </button>
            </form>
          </div>
          <p className="mt-3 border-t border-line pt-3 text-xs text-muted">Tip: drag across any chart to zoom in; double-click it to zoom out.</p>
        </div>
      )}
    </div>
  )
}
