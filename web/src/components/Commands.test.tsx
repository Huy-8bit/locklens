import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { CommandsResponse } from '../api/types'
import { CommandMix, commandSeries } from './Commands'

const base: CommandsResponse = {
  from: '2026-10-24T12:00:00Z',
  to: '2026-10-24T12:05:00Z',
  step_seconds: 60,
  commands: [],
  totals: [
    { command: 'select', calls: 300, total_exec_ms: 60, rows: 300, statements: 3, mean_exec_ms: 0.2 },
    { command: 'insert', calls: 100, total_exec_ms: 140, rows: 100, statements: 1, mean_exec_ms: 1.4 },
  ],
  buckets: [
    { t: '2026-10-24T12:00:00Z', command: 'select', calls: 120, total_exec_ms: 24, rows: 120 },
    { t: '2026-10-24T12:00:00Z', command: 'insert', calls: 60, total_exec_ms: 84, rows: 60 },
    { t: '2026-10-24T12:01:00Z', command: 'select', calls: 180, total_exec_ms: 36, rows: 180 },
    { t: '2026-10-24T12:01:00Z', command: 'transaction', calls: 30, total_exec_ms: 0, rows: 0 },
  ],
  coverage_buckets: [
    { t: '2026-10-24T12:00:00Z', covered_seconds: 60 },
    { t: '2026-10-24T12:01:00Z', covered_seconds: 60 },
    { t: '2026-10-24T12:02:00Z', covered_seconds: 60 },
    { t: '2026-10-24T12:03:00Z', covered_seconds: 0 },
  ],
  coverage: { window_seconds: 300, covered_seconds: 180, collections: 12, resets: 0, evicted_entries: 0, hidden_entries_max: 0 },
  monitoring_user: 'observer',
  monitoring_hidden: true,
}

describe('commandSeries', () => {
  it('divides by covered time, shows zero for quiet buckets, and unknown without coverage', () => {
    const s = commandSeries(base, 'calls')
    expect(s.map((x) => x.key)).toEqual(['select', 'insert', 'other'])
    const select = s.find((x) => x.key === 'select')?.points.map((p) => p[1])
    expect(select).toEqual([2, 3, 0, null])
    const insert = s.find((x) => x.key === 'insert')?.points.map((p) => p[1])
    expect(insert).toEqual([1, 0, 0, null])
    const other = s.find((x) => x.key === 'other')?.points.map((p) => p[1])
    expect(other).toEqual([0, 0.5, 0, null])
  })

  it('breaks the line where buckets are missing', () => {
    const data = { ...base, coverage_buckets: [{ t: '2026-10-24T12:00:00Z', covered_seconds: 60 }, { t: '2026-10-24T12:04:00Z', covered_seconds: 60 }] }
    const pts = commandSeries(data, 'calls')[0]?.points ?? []
    expect(pts).toHaveLength(3)
    expect(pts[1]?.[1]).toBeNull()
  })
})

describe('CommandMix', () => {
  it('lists each type with its share and average time', () => {
    render(<CommandMix data={base} />)
    expect(screen.getByRole('table', { name: 'Workload by statement type' })).toBeInTheDocument()
    expect(screen.getByText('SELECT')).toBeInTheDocument()
    expect(screen.getByText('75%')).toBeInTheDocument()
    expect(screen.getByText('1.4 ms')).toBeInTheDocument()
    expect(screen.getByText('70%')).toBeInTheDocument()
  })

  it('says so when nothing was collected', () => {
    render(<CommandMix data={{ ...base, totals: [] }} />)
    expect(screen.getByText(/No statements were collected/)).toBeInTheDocument()
  })
})
