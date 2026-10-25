import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { liveState } from './scope'

export type StreamState = 'connecting' | 'live' | 'offline'

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
    const onTarget = (e: MessageEvent) => {
      if (!liveState.on) return
      try {
        const { target_id } = JSON.parse(e.data) as { target_id?: number }
        void qc.invalidateQueries({ predicate: (q) => current(q.queryKey) || (q.queryKey[1] === target_id && q.queryKey[2] === 'live') })
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
    return () => es.close()
  }, [enabled, qc])
  return state
}
