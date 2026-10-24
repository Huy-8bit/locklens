import { useCallback, useMemo } from 'react'
import { useSearchParams } from 'react-router'

export const RANGE_PRESETS = [
  { value: '15m', label: 'Last 15 minutes', seconds: 900 },
  { value: '1h', label: 'Last hour', seconds: 3600 },
  { value: '6h', label: 'Last 6 hours', seconds: 21600 },
  { value: '24h', label: 'Last 24 hours', seconds: 86400 },
  { value: '7d', label: 'Last 7 days', seconds: 604800 },
] as const

export type RangeValue = (typeof RANGE_PRESETS)[number]['value']

export interface TimeRange {
  /** Preset such as 1h, or null for an absolute range. */
  preset: RangeValue | null
  from: string | null
  to: string | null
  /** Query parameters for the API. */
  params: Record<string, string>
  seconds: number
}

function goDuration(v: RangeValue): string {
  // The API takes Go durations; days are expressed in hours.
  return v === '7d' ? '168h' : v
}

/** Reads and writes the time range in the URL (?range=1h or ?from=&to=). */
export function useTimeRange(defaultPreset: RangeValue = '1h'): [TimeRange, (r: { preset?: RangeValue; from?: string; to?: string }) => void] {
  const [params, setParams] = useSearchParams()
  const range = useMemo<TimeRange>(() => {
    const from = params.get('from')
    const to = params.get('to')
    if (from && to && !Number.isNaN(Date.parse(from)) && !Number.isNaN(Date.parse(to)) && Date.parse(from) < Date.parse(to)) {
      const apiParams: Record<string, string> = { from, to }
      return { preset: null, from, to, params: apiParams, seconds: (Date.parse(to) - Date.parse(from)) / 1000 }
    }
    const raw = params.get('range')
    const preset = RANGE_PRESETS.find((p) => p.value === raw) ?? RANGE_PRESETS.find((p) => p.value === defaultPreset) ?? RANGE_PRESETS[1]
    const apiParams: Record<string, string> = { range: goDuration(preset.value) }
    return { preset: preset.value, from: null, to: null, params: apiParams, seconds: preset.seconds }
  }, [params, defaultPreset])
  const set = useCallback(
    (r: { preset?: RangeValue; from?: string; to?: string }) => {
      setParams(
        (prev) => {
          const next = new URLSearchParams(prev)
          next.delete('page')
          if (r.preset) {
            next.set('range', r.preset)
            next.delete('from')
            next.delete('to')
          } else if (r.from && r.to) {
            next.set('from', r.from)
            next.set('to', r.to)
            next.delete('range')
          }
          return next
        },
        { replace: true },
      )
    },
    [setParams],
  )
  return [range, set]
}
