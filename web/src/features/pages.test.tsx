import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router'
import { App } from '../app/App'
import { AuthProvider } from '../app/auth'
import { axeViolations, mockFetch, renderWithProviders } from '../test/utils'
import { IncidentsPage } from './IncidentsPage'
import { QueriesPage } from './QueriesPage'
import { TargetFormPage } from './SettingsPage'
import { FleetPage } from './FleetPage'
import type { Target } from '../api/types'

const session = { username: 'admin', csrf_token: 'tok', expires_at: '2026-10-25T00:00:00Z', server: { version: 'test', demo_mode: false, time: '' } }

const target: Target = {
  id: 1, name: 'prod', description: '', host: 'db', port: 5432, database: 'app', user: 'observer', enabled: true, interval_seconds: 10,
  query_text_enabled: false, max_connections: 2, node_exporter_url: null, created_at: '', updated_at: '',
  status: { state: 'degraded', server_version: '17.6', is_standby: false, capabilities: null, collectors: {}, last_attempt_at: null, last_success_at: null, last_error: null, last_error_at: null, freshness_seconds: null },
}

describe('authentication', () => {
  it('redirects to login, rejects wrong credentials, and signs in', async () => {
    let loggedIn = false
    const calls = mockFetch({
      '/api/v1/session': () => (loggedIn ? { body: session } : { status: 401, body: { error: { code: 'unauthenticated', message: 'login required' } } }),
      '/api/v1/auth/login': (init) => {
        const body = JSON.parse(String(init?.body)) as { password: string }
        if (body.password !== 'right password!') return { status: 401, body: { error: { code: 'invalid_credentials', message: 'invalid username or password' } } }
        loggedIn = true
        return { body: session }
      },
      '/api/v1/fleet': () => ({ body: { targets: [], generated_at: '' } }),
    })
    renderWithProviders(<AuthProvider><App /></AuthProvider>, { route: '/' })
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText('Username'), 'admin')
    await user.type(screen.getByLabelText('Password'), 'wrong')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('invalid username or password')
    await user.clear(screen.getByLabelText('Password'))
    await user.type(screen.getByLabelText('Password'), 'right password!')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))
    expect(await screen.findByRole('heading', { name: 'Fleet overview' })).toBeInTheDocument()
    expect(await screen.findByText('No targets yet')).toBeInTheDocument()
    // The CSRF token is sent on mutations only, from memory.
    const login = calls.find((c) => c.url === '/api/v1/auth/login')
    expect(new Headers(login?.init?.headers).get('X-CSRF-Token')).toBeNull()
    expect(localStorage.length === 0 || !Object.keys(localStorage).some((k) => k.includes('csrf'))).toBe(true)
  })
})

describe('fleet', () => {
  it('shows unknown instead of zero for missing values', async () => {
    mockFetch({ '/api/v1/fleet': () => ({ body: { targets: [{ target, latest: {}, counts: { open_incidents: 0, critical_findings: 0, warning_findings: 0, info_findings: 0 } }], generated_at: '' } }) })
    const { container } = renderWithProviders(<FleetPage />)
    expect(await screen.findByText('prod')).toBeInTheDocument()
    expect(screen.getAllByText('unknown').length).toBeGreaterThanOrEqual(2)
    expect(screen.getByText('degraded')).toBeInTheDocument()
    expect(await axeViolations(container)).toEqual([])
  })
  it('renders an error state', async () => {
    mockFetch({ '/api/v1/fleet': () => ({ status: 500, body: { error: { code: 'internal', message: 'internal error', request_id: 'abc' } } }) })
    renderWithProviders(<FleetPage />)
    expect(await screen.findByRole('alert')).toHaveTextContent('request abc')
  })
})

describe('target management', () => {
  it('shows server-side field validation and keeps the DSN write-only', async () => {
    let body: Record<string, unknown> = {}
    mockFetch({
      '/api/v1/targets': (init) => {
        body = JSON.parse(String(init?.body)) as Record<string, unknown>
        return { status: 422, body: { error: { code: 'validation', message: 'invalid target', fields: { dsn: 'invalid connection string (the value is not shown to protect credentials)', interval_seconds: 'must be between 5 and 3600' } } } }
      },
    })
    renderWithProviders(<Routes><Route path="/settings/targets/new" element={<TargetFormPage />} /></Routes>, { route: '/settings/targets/new' })
    const user = userEvent.setup()
    await user.type(screen.getByLabelText('Name'), 'new target')
    const dsn = screen.getByLabelText(/Connection string/)
    expect(dsn).toHaveAttribute('type', 'password')
    await user.type(dsn, 'postgres://bad')
    await user.click(screen.getByRole('button', { name: 'Add target' }))
    expect(await screen.findByText(/value is not shown/)).toBeInTheDocument()
    expect(screen.getByText('must be between 5 and 3600')).toBeInTheDocument()
    expect(body.query_text_enabled).toBe(false)
  })

  it('asks for confirmation before enabling query text collection', async () => {
    mockFetch({ '/api/v1/targets': () => ({ status: 201, body: target }) })
    renderWithProviders(<Routes><Route path="/settings/targets/new" element={<TargetFormPage />} /><Route path="/targets/1" element={<p>done</p>} /></Routes>, { route: '/settings/targets/new' })
    const user = userEvent.setup()
    await user.type(screen.getByLabelText('Name'), 'x')
    await user.type(screen.getByLabelText(/Connection string/), 'postgres://u@h/db')
    await user.click(screen.getByLabelText(/Collect query text/))
    await user.click(screen.getByRole('button', { name: 'Add target' }))
    const dialog = await screen.findByRole('dialog', { hidden: true })
    expect(within(dialog).getByText(/personal data/)).toBeInTheDocument()
  })
})

describe('incidents and queries', () => {
  it('shows an empty incident list', async () => {
    mockFetch({ '/api/v1/incidents': () => ({ body: { incidents: [], total: 0, page_size: 25, rules: ['blocking.long_chain'] } }), '/api/v1/targets': () => ({ body: { targets: [] } }) })
    renderWithProviders(<IncidentsPage />)
    expect(await screen.findByText('No incidents match')).toBeInTheDocument()
  })

  it('explains unavailable pg_stat_statements and small samples', async () => {
    mockFetch({
      '/api/v1/targets/1/queries': () => ({
        body: {
          from: '2026-10-24T09:00:00Z', to: '2026-10-24T10:00:00Z', total: 1, page_size: 25, query_text_enabled: false, text_included: false, min_calls: 50, monitoring_user: 'observer', monitoring_hidden: true,
          available: true, coverage: { window_seconds: 3600, covered_seconds: 1800, collections: 30, resets: 1, evicted_entries: 4, hidden_entries_max: 0 },
          items: [{ identity_id: 7, server_major: 17, datname: 'app', username: 'u', toplevel: true, queryid: '-9193564501488909311', query_text: null, calls: 3, total_exec_ms: 3, rows: 3, shared_blks_hit: 0, shared_blks_read: 0, temp_blks_written: 0, wal_bytes: null, share_of_total_time: 1, previous_calls: null, previous_total_exec_ms: null, mean_exec_ms: 1, calls_per_second: 0.001, previous_mean_exec_ms: null, comparison: { mean_ratio: null, calls_ratio: null, total_time_ratio: null, mean_regression: false, volume_increase: false, load_increase: false, too_few_calls: true } }],
        },
      }),
      '/api/v1/targets/1': () => ({ body: target }),
    })
    renderWithProviders(<Routes><Route path="/targets/:id/queries" element={<QueriesPage />} /></Routes>, { route: '/targets/1/queries' })
    expect(await screen.findByText('-9193564501488909311')).toBeInTheDocument()
    expect(screen.getByText('too few calls to compare')).toBeInTheDocument()
    expect(screen.getByText(/1 statistics reset/)).toBeInTheDocument()
    expect(screen.getByText(/4 evicted entries/)).toBeInTheDocument()
    expect(screen.getByText('unknown')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByLabelText('Show normalised query text')).toBeDisabled())
  })
})
