import type { QueryDetailResponse } from '../api/types'

/** Calls per second and mean time per call from a statement's buckets; a missing bucket breaks the lines. */
export function bucketSeries(d: QueryDetailResponse) {
  const step = d.step_seconds * 1000
  const calls: [number, number | null][] = []
  const mean: [number, number | null][] = []
  let prev: number | null = null
  for (const b of d.buckets) {
    const t = Date.parse(b.t)
    if (prev !== null && t - prev > 1.5 * step) {
      calls.push([prev + step, null])
      mean.push([prev + step, null])
    }
    const known = !b.gap && b.covered_seconds > 0
    calls.push([t, known ? b.calls / b.covered_seconds : null])
    mean.push([t, known && b.calls > 0 ? b.total_exec_ms / b.calls : null])
    prev = t
  }
  return { calls, mean }
}
