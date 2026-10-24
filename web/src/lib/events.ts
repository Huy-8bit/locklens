import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'

export type StreamState = 'connecting' | 'connected' | 'disconnected'

/**
 * Subscribes to server-sent events and invalidates affected queries. When the
 * stream drops, the browser reconnects automatically and the UI reports the
 * disconnected state; queries keep polling as a fallback.
 */
export function useServerEvents(enabled: boolean): StreamState {
  const qc = useQueryClient()
  const [state, setState] = useState<StreamState>('connecting')
  useEffect(() => {
    if (!enabled || typeof EventSource === 'undefined') return
    const es = new EventSource('/api/v1/events')
    let timer: ReturnType<typeof setTimeout> | undefined
    const pending = new Set<string>()
    const flush = () => {
      timer = undefined
      for (const key of pending) {
        void qc.invalidateQueries({ queryKey: [key] })
      }
      pending.clear()
    }
    const schedule = (...keys: string[]) => {
      keys.forEach((k) => pending.add(k))
      // Coalesce bursts of events into one refetch per second.
      timer ??= setTimeout(flush, 1000)
    }
    es.onopen = () => setState('connected')
    es.onerror = () => setState(es.readyState === EventSource.CLOSED ? 'disconnected' : 'connecting')
    es.addEventListener('target.collected', () => schedule('fleet', 'overview', 'activity', 'findings', 'target', 'replication', 'host'))
    es.addEventListener('target.status', () => schedule('fleet', 'target', 'overview'))
    for (const k of ['incident.opened', 'incident.updated', 'incident.resolved']) {
      es.addEventListener(k, () => schedule('incidents', 'incident', 'incident-events', 'fleet', 'overview'))
    }
    return () => {
      if (timer) clearTimeout(timer)
      es.close()
    }
  }, [enabled, qc])
  return enabled ? state : 'disconnected'
}
