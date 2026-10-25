import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'

// The global scope of the UI: which environment is shown, the time range of
// history views, and whether data refreshes live. Environment, a relative
// range, and Live are remembered per browser (a convenience only; nothing
// else is stored locally). An absolute range (from zooming) is kept in
// memory only.

export const RANGES = [
  { value: '15m', label: 'Last 15 minutes', seconds: 900 },
  { value: '1h', label: 'Last hour', seconds: 3600 },
  { value: '6h', label: 'Last 6 hours', seconds: 21600 },
  { value: '24h', label: 'Last 24 hours', seconds: 86400 },
  { value: '168h', label: 'Last 7 days', seconds: 604800 },
] as const

export type RelativeRange = (typeof RANGES)[number]['value']

/**
 * A relative range ("1h": the last hour) or an absolute window written as
 * "abs:<from ms>-<to ms>" (for example after zooming into a chart). Query
 * hooks turn it into API parameters with rangeQuery().
 */
export type Range = RelativeRange | `abs:${number}-${number}`

/** The shortest window a zoom can produce, and the longest the API accepts. */
export const MIN_WINDOW_MS = 60_000
export const MAX_WINDOW_MS = 31 * 86_400_000

export function absolute(from: number, to: number): Range {
  const f = Math.round(Math.min(from, to))
  const t = Math.round(Math.max(from, to))
  return `abs:${f}-${Math.max(t, f + 1000)}`
}

export function isAbsolute(r: Range): r is `abs:${number}-${number}` {
  return r.startsWith('abs:')
}

/** The window of a range in epoch milliseconds (relative ranges end now). */
export function rangeWindow(r: Range, now = Date.now()): [number, number] {
  if (isAbsolute(r)) {
    const [f, t] = r.slice(4).split('-').map(Number)
    return [f ?? now - 3_600_000, t ?? now]
  }
  const s = RANGES.find((x) => x.value === r)?.seconds ?? 3600
  return [now - s * 1000, now]
}

/** API query parameters of a range. */
export function rangeQuery(r: Range): { range?: string; from?: string; to?: string } {
  if (!isAbsolute(r)) return { range: r }
  const [f, t] = rangeWindow(r)
  return { from: new Date(f).toISOString(), to: new Date(t).toISOString() }
}

function clock(ms: number, withDate: boolean): string {
  const d = new Date(ms)
  const time = d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
  return withDate ? `${d.toLocaleDateString('en-CA')} ${time}` : time
}

export function rangeLabel(r: Range): string {
  if (!isAbsolute(r)) return RANGES.find((x) => x.value === r)?.label ?? r
  const [f, t] = rangeWindow(r)
  const sameDay = new Date(f).toDateString() === new Date(t).toDateString() && new Date(t).toDateString() === new Date().toDateString()
  return `${clock(f, !sameDay)} → ${clock(t, !sameDay)}`
}

/** Twice as long, around the same center (Grafana's "zoom out"). */
export function zoomedOut(r: Range, now = Date.now()): Range {
  const [f, t] = rangeWindow(r, now)
  const span = Math.min((t - f) * 2, MAX_WINDOW_MS)
  const center = (f + t) / 2
  let to = Math.min(center + span / 2, now)
  const from = to - span
  to = from + span
  // A window that reaches now becomes the matching relative range again.
  const rel = RANGES.find((x) => Math.abs(x.seconds * 1000 - span) < 1000)
  if (rel && now - to < 5000) return rel.value
  return absolute(from, to)
}

/** Moved by half a window, never past now. */
export function shifted(r: Range, direction: -1 | 1, now = Date.now()): Range {
  const [f, t] = rangeWindow(r, now)
  const step = ((t - f) / 2) * direction
  let from = f + step
  let to = t + step
  if (to > now) {
    from -= to - now
    to = now
  }
  return absolute(from, to)
}

interface Scope {
  /** Environment label, or '' for all environments. */
  env: string
  range: Range
  live: boolean
  /** Earlier ranges, most recent last (for "back" after zooming). */
  history: Range[]
  setEnv: (e: string) => void
  /** Sets the range; zoom and shift actions remember the previous one. */
  setRange: (r: Range, remember?: boolean) => void
  /** Returns to the range before the last zoom or shift. */
  back: () => void
  setLive: (l: boolean) => void
}

const KEY = 'locklens-scope'

/** Read by query hooks to pause refetching without re-rendering them. */
export const liveState = { on: true }

function load(): { env: string; range: Range; live: boolean } {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? '{}') as { env?: unknown; range?: unknown; live?: unknown }
    const range = RANGES.find((r) => r.value === v.range)?.value ?? '1h'
    return { env: typeof v.env === 'string' ? v.env : '', range, live: v.live !== false }
  } catch {
    return { env: '', range: '1h', live: true }
  }
}

const Ctx = createContext<Scope | null>(null)

export function ScopeProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState(() => ({ ...load(), history: [] as Range[] }))
  useEffect(() => {
    liveState.on = state.live
  }, [state.live])
  const update = useCallback((fn: (s: typeof state) => typeof state) => {
    setState((s) => {
      const next = fn(s)
      try {
        // An absolute window is not remembered: the stored relative range stays.
        const range = isAbsolute(next.range) ? load().range : next.range
        localStorage.setItem(KEY, JSON.stringify({ env: next.env, live: next.live, range }))
      } catch {
        // storage unavailable: keep the choice for this page only
      }
      liveState.on = next.live
      return next
    })
  }, [])
  const value = useMemo<Scope>(
    () => ({
      ...state,
      setEnv: (env) => update((s) => ({ ...s, env })),
      setRange: (range, remember = false) => update((s) => (range === s.range ? s : { ...s, range, history: remember ? [...s.history, s.range].slice(-20) : [] })),
      back: () => update((s) => (s.history.length ? { ...s, range: s.history[s.history.length - 1] as Range, history: s.history.slice(0, -1) } : s)),
      setLive: (live) => update((s) => ({ ...s, live })),
    }),
    [state, update],
  )
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useScope(): Scope {
  const v = useContext(Ctx)
  if (!v) throw new Error('useScope outside ScopeProvider')
  return v
}

/** The global time range and its setter. */
export function useRange(): [Range, (r: Range) => void] {
  const s = useScope()
  return [s.range, s.setRange]
}
