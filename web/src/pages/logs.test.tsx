import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { LogSummary } from '../api/types'
import { axeViolations, mockFetch, renderAt } from '../test/utils'
import LogsPage from './LogsPage'

afterEach(() => vi.unstubAllGlobals())

const summary: LogSummary = {
  from: '2026-10-30T11:00:00Z',
  to: '2026-10-30T12:00:00Z',
  step_seconds: 60,
  counts: [{ t: '2026-10-30T11:30:00Z', kind: 'slow_query', n: 3 }],
  statements: [
    { fingerprint: 'fp1', source: 'slow_query', query: 'SELECT * FROM orders WHERE id = $1', query_id: 42, count: 3, total_ms: 3000, p50_ms: 900, p95_ms: 1200, p99_ms: 1250, max_ms: 1260, last_at: '2026-10-30T11:59:00Z', plans: 1 },
  ],
  agents: [],
  settings: [
    { name: 'log_min_duration_statement', value: '500', ok: true, advice: 'Logs statements slower than this (ms).' },
    { name: 'log_lock_waits', value: 'off', ok: false, advice: 'Logs sessions waiting for a lock.' },
  ],
}

describe('LogsPage', () => {
  it('shows setup, logged statement percentiles with their threshold, and plans', async () => {
    mockFetch({
      '/api/v1/targets/1/logs': () => ({ body: summary }),
      '/api/v1/targets/1/logs/events': (_init, url) =>        url.search.includes('fingerprint=fp1')
          ? { body: { events: [{ id: 'p1', at: '2026-10-30T11:58:00Z', kind: 'plan', severity: 'LOG', duration_ms: 1200, message: 'plan', details: { plan: 'Seq Scan on orders\n  Filter: (id = ?)' } }] } }
          : { body: { events: [] } },
    })
    const { container } = renderAt('/databases/1/logs', '/databases/:id/logs', <LogsPage />)
    expect(await screen.findByText(/No log agent sends this instance's logs yet/)).toBeInTheDocument()
    expect(screen.getByText(/slower than log_min_duration_statement = 500 ms/)).toBeInTheDocument()
    expect(screen.getByText('log_lock_waits = off')).toBeInTheDocument()
    const table = screen.getByRole('table')
    const row = within(table).getAllByRole('row')[1] as HTMLElement
    await userEvent.setup().click(row)
    const dialog = await screen.findByRole('dialog')
    expect(await within(dialog).findByText(/Seq Scan on orders/)).toBeInTheDocument()
    expect(within(dialog).getByText('queryid 42 (pg_stat_statements)')).toBeInTheDocument()
    expect(await axeViolations(container)).toEqual([])
  })
})
