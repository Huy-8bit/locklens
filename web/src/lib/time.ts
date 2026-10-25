import { useSearchParams } from 'react-router'

// Time ranges for history views live in the URL (?range=1h), so a view can
// be shared and survives reloads.

export const RANGES = [
  { value: '15m', label: 'Last 15 minutes', seconds: 900 },
  { value: '1h', label: 'Last hour', seconds: 3600 },
  { value: '6h', label: 'Last 6 hours', seconds: 21600 },
  { value: '24h', label: 'Last 24 hours', seconds: 86400 },
  { value: '168h', label: 'Last 7 days', seconds: 604800 },
] as const

export type Range = (typeof RANGES)[number]['value']

export function useRange(fallback: Range = '15m'): [Range, (r: Range) => void] {
  const [params, setParams] = useSearchParams()
  const v = params.get('range')
  const range = (RANGES.find((r) => r.value === v)?.value ?? fallback) as Range
  const set = (r: Range) =>
    setParams(
      (p) => {
        const n = new URLSearchParams(p)
        n.set('range', r)
        return n
      },
      { replace: true },
    )
  return [range, set]
}

export function rangeSeconds(r: Range): number {
  return RANGES.find((x) => x.value === r)?.seconds ?? 3600
}
