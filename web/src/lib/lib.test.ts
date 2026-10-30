import { describe, expect, it } from 'vitest'
import { toMarkers } from './changes'
import { commandColor, commandGroup } from './commands'
import { bytes, compact, ms, num, pct, rate, seconds, UNKNOWN } from './format'
import { absolute, isAbsolute, rangeQuery, rangeWindow, shifted, zoomedOut } from './scope'
import { findingQueryKey, groupTimeline, queryKey } from './incident'
import type { Finding, IncidentEvent } from '../api/types'

describe('format', () => {
  it('renders unknown values as unknown, never as zero', () => {
    for (const f of [num, compact, rate, pct, bytes, ms, seconds]) {
      expect(f(null)).toBe(UNKNOWN)
      expect(f(undefined)).toBe(UNKNOWN)
      expect(f(Number.NaN)).toBe(UNKNOWN)
    }
  })
  it('keeps real zero', () => {
    expect(num(0)).toBe('0')
    expect(bytes(0)).toBe('0 B')
    expect(pct(0)).toBe('0.0%')
  })
  it('formats magnitudes', () => {
    expect(compact(1530)).toBe('1.5k')
    expect(compact(2_400_000)).toBe('2.4M')
    expect(rate(0.004)).toBe('<0.01/s')
    expect(bytes(1536)).toBe('1.5 KiB')
    expect(ms(0.42)).toBe('0.42 ms')
    expect(ms(1500)).toBe('1.5 s')
    expect(seconds(125)).toBe('2m 5s')
  })
})

describe('command colors', () => {
  it('assigns a fixed color per class and folds rare classes into other', () => {
    expect(commandColor('select')).toBe('var(--s1)')
    expect(commandColor('delete')).toBe('var(--s4)')
    expect(commandColor('transaction')).toBe('var(--s-other)')
    expect(commandGroup('utility')).toBe('other')
    expect(commandGroup('insert')).toBe('insert')
  })
})

describe('time ranges', () => {
  const now = Date.parse('2026-10-25T12:00:00Z')
  const min = 60_000

  it('turns ranges into API parameters', () => {
    expect(rangeQuery('1h')).toEqual({ range: '1h' })
    const r = absolute(now - 30 * min, now - 10 * min)
    expect(isAbsolute(r)).toBe(true)
    expect(rangeQuery(r)).toEqual({ from: '2026-10-25T11:30:00.000Z', to: '2026-10-25T11:50:00.000Z' })
    expect(rangeWindow('15m', now)).toEqual([now - 15 * min, now])
  })

  it('orders and rounds an absolute window', () => {
    expect(rangeWindow(absolute(now, now - min), now)).toEqual([now - min, now])
  })

  it('zooms out around the center and returns to a relative range at now', () => {
    expect(rangeWindow(zoomedOut(absolute(now - 50 * min, now - 40 * min), now), now)).toEqual([now - 55 * min, now - 35 * min])
    // 30 minutes ending now doubles to "the last hour".
    expect(zoomedOut(absolute(now - 30 * min, now), now)).toBe('1h')
    // Never past now: the window shifts back instead.
    expect(rangeWindow(zoomedOut(absolute(now - 20 * min, now - 2 * min), now), now)).toEqual([now - 36 * min, now])
  })

  it('moves by half a window and stops at now', () => {
    expect(rangeWindow(shifted('1h', -1, now), now)).toEqual([now - 90 * min, now - 30 * min])
    expect(rangeWindow(shifted(absolute(now - 90 * min, now - 30 * min), 1, now), now)).toEqual([now - 60 * min, now])
    expect(rangeWindow(shifted(absolute(now - 70 * min, now - 10 * min), 1, now), now)).toEqual([now - 60 * min, now])
  })
})


describe('incident timeline', () => {
  const ev = (id: number, kind: string, summary = 's'): IncidentEvent => ({ id, at: `2026-10-25T13:00:${String(id).padStart(2, '0')}Z`, kind, summary, payload: {} })

  it('collapses runs of identical routine samples', () => {
    const rows = groupTimeline([ev(1, 'pre_trigger'), ev(2, 'pre_trigger'), ev(3, 'pre_trigger'), ev(4, 'trigger'), ev(5, 'sample'), ev(6, 'sample', 'changed')])
    expect(rows.map((r) => (r.type === 'group' ? `${r.kind}x${r.events.length}` : r.event.kind))).toEqual(['pre_triggerx3', 'trigger', 'sample', 'sample'])
  })

  it('never collapses lifecycle events', () => {
    const rows = groupTimeline([ev(1, 'trigger'), ev(2, 'trigger'), ev(3, 'resolved'), ev(4, 'resolved')])
    expect(rows.every((r) => r.type === 'event')).toBe(true)
  })

  it('matches statement findings to statement keys', () => {
    const key = { server_major: 18, dbid: 16384, userid: 10, toplevel: true, queryid: '6328518588233945134' }
    expect(findingQueryKey({ key: `q:${queryKey(key)}` } as Finding)).toBe(queryKey(key))
    expect(findingQueryKey({ key: 'root:42' } as Finding)).toBeNull()
  })
})

describe('change markers', () => {
  it('names each change by its kind and title at its time', () => {
    const m = toMarkers([
      { id: 1, target_id: 1, at: '2026-10-30T10:00:00Z', kind: 'deploy', source: 'api', title: 'api v2', details: {}, created_by: 'token:ci' },
      { id: 2, target_id: null, at: '2026-10-30T11:00:00Z', kind: 'settings', source: 'detected', title: 'Setting work_mem changed', details: {}, created_by: '' },
    ])
    expect(m).toEqual([
      { at: Date.parse('2026-10-30T10:00:00Z'), label: 'Deploy: api v2' },
      { at: Date.parse('2026-10-30T11:00:00Z'), label: 'Settings: Setting work_mem changed' },
    ])
  })
})
