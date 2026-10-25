import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'

// The global scope of the UI: which environment is shown, the time range of
// history views, and whether data refreshes live. Remembered per browser
// (a convenience only; nothing else is stored locally).

export const RANGES = [
  { value: '15m', label: 'Last 15 minutes', seconds: 900 },
  { value: '1h', label: 'Last hour', seconds: 3600 },
  { value: '6h', label: 'Last 6 hours', seconds: 21600 },
  { value: '24h', label: 'Last 24 hours', seconds: 86400 },
  { value: '168h', label: 'Last 7 days', seconds: 604800 },
] as const

export type Range = (typeof RANGES)[number]['value']

interface Scope {
  /** Environment label, or '' for all environments. */
  env: string
  range: Range
  live: boolean
  setEnv: (e: string) => void
  setRange: (r: Range) => void
  setLive: (l: boolean) => void
}

const KEY = 'locklens-scope'

/** Read by query hooks to pause refetching without re-rendering them. */
export const liveState = { on: true }

function load(): Pick<Scope, 'env' | 'range' | 'live'> {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? '{}') as Partial<Scope>
    const range = RANGES.find((r) => r.value === v.range)?.value ?? '1h'
    return { env: typeof v.env === 'string' ? v.env : '', range, live: v.live !== false }
  } catch {
    return { env: '', range: '1h', live: true }
  }
}

const Ctx = createContext<Scope | null>(null)

export function ScopeProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState(load)
  useEffect(() => {
    liveState.on = state.live
  }, [state.live])
  const update = useCallback((patch: Partial<Pick<Scope, 'env' | 'range' | 'live'>>) => {
    setState((s) => {
      const next = { ...s, ...patch }
      try {
        localStorage.setItem(KEY, JSON.stringify(next))
      } catch {
        // storage unavailable: keep the choice for this page only
      }
      liveState.on = next.live
      return next
    })
  }, [])
  const value = useMemo<Scope>(
    () => ({ ...state, setEnv: (env) => update({ env }), setRange: (range) => update({ range }), setLive: (live) => update({ live }) }),
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

export function rangeLabel(r: Range): string {
  return RANGES.find((x) => x.value === r)?.label ?? r
}
