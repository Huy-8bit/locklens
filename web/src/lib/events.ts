import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { liveState } from './scope'

export type StreamState = 'connecting' | 'live' | 'offline'

/** The shortest time between two live-tail refreshes of one target's history views. */
export const TAIL_MS = 10_000

/** Collections that add points to history charts. */
const TAIL_COLLECTORS = new Set(['activity', 'database', 'statements', 'host'])

/** Whether a query key is for a short relative range (live tail). */
export function shortRange(key: readonly unknown[]): boolean {
  return key.some((x) => x === '15m' || x === '1h')
}

/**
 * Subscribes to the server's event stream. Events carry no data: they tell
 * the UI which cached queries are stale, so pages refresh as soon as a target
 * finishes a collection instead of polling everything.
 */
export function useEventStream(enabled: boolean): StreamState {
  const qc = useQueryClient()
  const [state, setState] = useState<StreamState>('connecting')
  useEffect(() => {
    if (!enabled || typeof EventSource === 'undefined') return
    const es = new EventSource('/api/v1/events')
    es.onopen = () => setState('live')
    es.onerror = () => setState('offline')
    // History views (fleet metrics and queries) refresh on their own slower
    // interval; a collection only invalidates current-state views. Nothing
    // refreshes while the Live switch is off.
    const current = (k: readonly unknown[]) => (k[0] === 'fleet' && k[1] !== 'metrics' && k[1] !== 'queries') || k[0] === 'targets' || k[0] === 'system'
    // Live tail: history views of a short relative range ("15m", "1h") also
    // follow new collections, at most every TAIL_MS per target, so their
    // charts grow with every sample instead of every 30 seconds.
    const lastTail = new Map<string, number>()
    const due = (key: string, now: number) => {
      if (now - (lastTail.get(key) ?? 0) < TAIL_MS) return false
      lastTail.set(key, now)
      return true
    }
    const onTarget = (e: MessageEvent) => {
      if (!liveState.on) return
      try {
        const { target_id, collector } = JSON.parse(e.data) as { target_id?: number; collector?: string }
        const now = Date.now()
        const tail = collector !== undefined && TAIL_COLLECTORS.has(collector)
        const targetTail = tail && due(`t${target_id}`, now)
        const fleetTail = tail && due('fleet', now)
        void qc.invalidateQueries({
          predicate: (q) => {
            const k = q.queryKey
            if (current(k) || (k[1] === target_id && k[2] === 'live')) return true
            if (targetTail && k[0] === 't' && k[1] === target_id && k[2] === 'hist') return shortRange(k)
            return fleetTail && k[0] === 'fleet' && shortRange(k)
          },
        })
      } catch {
        // ignore malformed events
      }
    }
    const onIncident = () => {
      if (liveState.on) void qc.invalidateQueries({ predicate: (q) => q.queryKey[0] === 'incidents' || current(q.queryKey) || q.queryKey[2] === 'live' })
    }
    es.addEventListener('target.collected', onTarget)
    es.addEventListener('target.status', onTarget)
    es.addEventListener('incident.updated', onIncident)
    es.addEventListener('recommendation.updated', () => {
      if (liveState.on) void qc.invalidateQueries({ predicate: (q) => q.queryKey[0] === 'optimizer' || q.queryKey[3] === 'query-insight' })
    })
    return () => es.close()
  }, [enabled, qc])
  return state
}
