import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AuthProvider } from '../auth'
import type { CommandsResponse, OverviewResponse, Target } from '../api/types'
import { axeViolations, mockFetch, renderAt } from '../test/utils'
import { LoginPage } from './LoginPage'
import OverviewPage from './OverviewPage'
import ExplorerPage from './ExplorerPage'
import QueriesPage from './QueriesPage'

afterEach(() => vi.unstubAllGlobals())

const target: Target = {
  id: 1,
  name: 'orders-db',
  description: '',
  host: 'db',
  port: 5432,
  database: 'app',
  user: 'observer',
  enabled: true,
  interval_seconds: 10,
  query_text_enabled: true,
  max_connections: 2,
  node_exporter_url: null,
  created_at: '2026-10-24T00:00:00Z',
  updated_at: '2026-10-24T00:00:00Z',
  status: { state: 'healthy', server_version: '18.6', is_standby: false, capabilities: null, collectors: {}, last_attempt_at: null, last_success_at: null, last_error: null, last_error_at: null, freshness_seconds: 1 },
}

const commands: CommandsResponse = {
  from: '2026-10-24T12:00:00Z',
  to: '2026-10-24T12:15:00Z',
  step_seconds: 60,
  commands: [],
  totals: [
    { command: 'select', calls: 900, total_exec_ms: 90, rows: 900, statements: 2, mean_exec_ms: 0.1 },
    { command: 'update', calls: 100, total_exec_ms: 50, rows: 100, statements: 1, mean_exec_ms: 0.5 },
  ],
  buckets: [],
  coverage_buckets: [],
  coverage: { window_seconds: 900, covered_seconds: 900, collections: 60, resets: 0, evicted_entries: 0, hidden_entries_max: 0 },
  monitoring_user: 'observer',
  monitoring_hidden: true,
  available: true,
}

const metrics = { body: { from: '2026-10-24T12:00:00Z', to: '2026-10-24T12:15:00Z', step_seconds: 60, series: [], gaps: [] } }

describe('LoginPage', () => {
  it('rejects wrong credentials and signs in', async () => {
    let attempts = 0
    const calls = mockFetch({
      '/api/v1/session': () => ({ status: 401, body: { error: { code: 'unauthenticated', message: 'login required' } } }),
      '/api/v1/auth/login': () => (++attempts === 1 ? { status: 401, body: { error: { code: 'invalid_credentials', message: 'Invalid user name or password.' } } } : { body: { username: 'admin', csrf_token: 'tok', expires_at: '', server: { version: 'dev', demo_mode: false, time: '' } } }),
    })
    const { container } = renderAt('/login', '/login', <AuthProvider><LoginPage /></AuthProvider>)
    const user = userEvent.setup()
    await user.type(screen.getByLabelText('Password'), 'wrong-password')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Invalid user name or password.')
    await user.clear(screen.getByLabelText('Password'))
    await user.type(screen.getByLabelText('Password'), 'right-password')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))
    await waitFor(() => expect(calls.filter((c) => c.url === '/api/v1/auth/login')).toHaveLength(2))
    expect(await axeViolations(container)).toEqual([])
  })
})

describe('OverviewPage', () => {
  it('shows unknown values as unknown and the query mix by type', async () => {
    const overview: OverviewResponse = { target, latest: { 'connections.used': { at: '', value: 12 }, 'connections.max': { at: '', value: 100 }, 'statements.calls.per_s': { at: '', value: null } }, connected: true, findings: [], open_incidents: [], open_incident_count: 0 }
    mockFetch({
      '/api/v1/targets/1/overview': () => ({ body: overview }),
      '/api/v1/targets/1/queries/commands': () => ({ body: commands }),
      '/api/v1/targets/1/queries': () => ({ body: { items: [], total: 0, page_size: 5, coverage: commands.coverage, query_text_enabled: true, text_included: true, min_calls: 50, monitoring_user: 'observer', monitoring_hidden: true, available: true, from: '', to: '' } }),
      '/api/v1/targets/1/activity': () => ({ body: { source: 'none', report: null } }),
      '/api/v1/targets/1/metrics': () => metrics,
    })
    const { container } = renderAt('/i/1/overview', '/i/:id/overview', <OverviewPage />)
    expect(await screen.findByRole('heading', { name: /orders-db/ })).toBeInTheDocument()
    const qps = screen.getByText('Queries per second').closest('div')?.parentElement
    expect(qps && within(qps).getByText('—')).toBeInTheDocument()
    expect(screen.getByText('12')).toBeInTheDocument()
    expect(await screen.findAllByRole('table', { name: 'Workload by statement type' })).not.toHaveLength(0)
    expect(screen.getByText('90%')).toBeInTheDocument()
    expect(await axeViolations(container)).toEqual([])
  })
})

describe('ExplorerPage', () => {
  it('asks for separate credentials before browsing and confirms row access', async () => {
    const calls = mockFetch({
      '/api/v1/targets/1/workspace/policy': (init) =>
        init?.method === 'PUT' ? { status: 400, body: { error: { code: 'invalid_dsn', message: 'Use the target host, port and database with a separate least-privilege role.' } } } : { body: { target_id: 1, metadata_enabled: false, data_enabled: false, sql_enabled: false, history_enabled: false, configured: false, updated_at: '' } },
      '/api/v1/targets/1/tables': () => ({ body: { source: 'none', tables: null } }),
    })
    renderAt('/i/1/explorer', '/i/:id/explorer', <ExplorerPage />)
    expect(await screen.findByText('Set up data access')).toBeInTheDocument()
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(/Connection string of the browsing role/), 'postgres://reader:secret@db/app')
    await user.click(screen.getByRole('checkbox', { name: /Browse table rows/ }))
    await user.click(screen.getByRole('button', { name: 'Save access' }))
    expect(screen.getByRole('alertdialog', { name: 'Allow browsing table rows?' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Allow' }))
    expect(await screen.findByText(/separate least-privilege role/)).toBeInTheDocument()
    const put = calls.find((c) => c.method === 'PUT')
    expect(put && JSON.parse(put.body ?? '{}')).toMatchObject({ data_enabled: true, sql_enabled: false })
  })
})

describe('QueriesPage', () => {
  it('filters by statement type and shows the SQL', async () => {
    const calls = mockFetch({
      '/api/v1/targets/1/queries/commands': () => ({ body: commands }),
      '/api/v1/targets/1/queries': () => ({
        body: {
          from: '',
          to: '',
          items: [{ identity_id: 7, server_major: 18, datname: 'app', username: 'web', toplevel: true, queryid: '-12345678901234567', command: 'update', query_text: 'UPDATE items SET qty = $1 WHERE id = $2', calls: 100, total_exec_ms: 50, rows: 100, shared_blks_hit: 0, shared_blks_read: 0, temp_blks_written: 0, wal_bytes: null, share_of_total_time: 0.35, previous_calls: null, previous_total_exec_ms: null, mean_exec_ms: 0.5, calls_per_second: 0.11, previous_mean_exec_ms: null, comparison: null }],
          total: 1,
          page_size: 25,
          coverage: commands.coverage,
          query_text_enabled: true,
          text_included: true,
          min_calls: 50,
          monitoring_user: 'observer',
          monitoring_hidden: true,
          available: true,
        },
      }),
    })
    const { container } = renderAt('/i/1/queries', '/i/:id/queries', <QueriesPage />)
    expect(await screen.findByTitle('UPDATE items SET qty = $1 WHERE id = $2')).toBeInTheDocument()
    const group = screen.getByRole('group', { name: 'Statement type' })
    // Only types present in the window are offered.
    expect(within(group).getAllByRole('button').map((b) => b.textContent)).toEqual(['All types', 'SELECT', 'UPDATE'])
    await userEvent.setup().click(within(group).getByRole('button', { name: 'UPDATE' }))
    await waitFor(() => expect(calls.some((c) => c.url.startsWith('/api/v1/targets/1/queries?') && c.url.includes('command=update'))).toBe(true))
    expect(await axeViolations(container)).toEqual([])
  })
})
