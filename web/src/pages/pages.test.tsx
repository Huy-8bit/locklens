import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AuthProvider } from '../auth'
import type { CommandsResponse, FleetLiveResponse, LoadResponse, OverviewResponse, SetupPlan, Target } from '../api/types'
import { axeViolations, mockFetch, renderAt, sessionFor } from '../test/utils'
import { LoginPage } from './LoginPage'
import OverviewPage from './OverviewPage'
import { DatabaseOverview } from './DatabasePage'
import SqlPage from './SqlPage'
import QuickAddPage from './QuickAddPage'
import SettingsPage from './SettingsPage'
import ExplorerPage from './ExplorerPage'
import QueriesPage from './QueriesPage'
import LoadPage from './LoadPage'
import { NotificationsCard } from '../components/NotificationsCard'

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
  environment: 'lab',
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

const live: FleetLiveResponse = {
  generated_at: '2026-10-24T12:15:00Z',
  targets: [
    {
      target,
      connected: true,
      observed_at: '2026-10-24T12:15:00Z',
      latest: { 'connections.used': { at: '', value: 7 }, 'connections.max': { at: '', value: 100 } },
      counts: null,
      report: null,
      replication: null,
      connection_groups: [],
      host: null,
    },
  ],
}

const sqlPolicy = { target_id: 1, metadata_enabled: false, data_enabled: false, sql_enabled: true, history_enabled: false, configured: true, updated_at: '' }

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
    await user.type(screen.getByLabelText('Username'), 'admin')
    await user.type(screen.getByLabelText('Password'), 'wrong-password')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('The username or password is not correct.')
    await user.clear(screen.getByLabelText('Password'))
    await user.type(screen.getByLabelText('Password'), 'right-password')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))
    await waitFor(() => expect(calls.filter((c) => c.url === '/api/v1/auth/login')).toHaveLength(2))
    expect(await axeViolations(container)).toEqual([])
  })
})

describe('DatabaseOverview', () => {
  it('shows unknown values as unknown and the query mix by type', async () => {
    const overview: OverviewResponse = { target, latest: { 'connections.used': { at: '', value: 12 }, 'connections.max': { at: '', value: 100 }, 'statements.calls.per_s': { at: '', value: null } }, connected: true, findings: [], open_incidents: [], open_incident_count: 0 }
    mockFetch({
      '/api/v1/targets/1/overview': () => ({ body: overview }),
      '/api/v1/targets/1/queries/commands': () => ({ body: commands }),
      '/api/v1/targets/1/queries': () => ({ body: { items: [], total: 0, page_size: 5, coverage: commands.coverage, query_text_enabled: true, text_included: true, min_calls: 50, monitoring_user: 'observer', monitoring_hidden: true, available: true, from: '', to: '' } }),
      '/api/v1/targets/1/activity': () => ({ body: { source: 'none', report: null } }),
      '/api/v1/targets/1/metrics': () => metrics,
    })
    const { container } = renderAt('/databases/1', '/databases/:id', <DatabaseOverview />)
    const qps = (await screen.findByText('Queries per second')).closest('div')?.parentElement
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
        init?.method === 'PUT' ? { status: 400, body: { error: { code: 'invalid_dsn', message: "Use the target's host, port, and database." } } } : { body: { target_id: 1, metadata_enabled: false, data_enabled: false, sql_enabled: false, history_enabled: false, configured: false, updated_at: '' } },
      '/api/v1/targets/1/tables': () => ({ body: { source: 'none', tables: null } }),
      '/api/v1/fleet/live': () => ({ body: live }),
    })
    renderAt('/explorer/1', '/explorer/:targetId', <ExplorerPage />)
    expect(await screen.findByText('Set up data access')).toBeInTheDocument()
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(/Connection string of the browsing role/), 'postgres://reader:secret@db/app')
    await user.click(screen.getByRole('checkbox', { name: /Browse table rows/ }))
    await user.click(screen.getByRole('button', { name: 'Save access' }))
    expect(screen.getByRole('alertdialog', { name: 'Allow reading data?' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Allow' }))
    expect(await screen.findByText(/target's host, port, and database/)).toBeInTheDocument()
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
    const { container } = renderAt('/databases/1/queries', '/databases/:id/queries', <QueriesPage />)
    expect(await screen.findByTitle('UPDATE items SET qty = $1 WHERE id = $2')).toBeInTheDocument()
    const group = screen.getByRole('group', { name: 'Statement type' })
    // Only types present in the window are offered.
    expect(within(group).getAllByRole('button').map((b) => b.textContent)).toEqual(['All types', 'SELECT', 'UPDATE'])
    await userEvent.setup().click(within(group).getByRole('button', { name: 'UPDATE' }))
    await waitFor(() => expect(calls.some((c) => c.url.startsWith('/api/v1/targets/1/queries?') && c.url.includes('command=update'))).toBe(true))
    expect(await axeViolations(container)).toEqual([])
  })
})

describe('OverviewPage', () => {
  it('summarises the fleet without inventing values', async () => {
    mockFetch({
      '/api/v1/fleet/live': () => ({ body: live }),
      '/api/v1/fleet/metrics': () => ({ body: { from: '2026-10-24T12:00:00Z', to: '2026-10-24T12:15:00Z', step_seconds: 60, targets: 1, series: [] } }),
      '/api/v1/fleet/queries': () => ({ body: { from: '', to: '', sort: 'total_time', items: [], targets: 1 } }),
      '/api/v1/incidents': () => ({ body: { incidents: [], total: 0, page_size: 50, rules: [] } }),
      '/api/v1/targets/1/workspace/policy': () => ({ body: { ...sqlPolicy, sql_enabled: false, configured: false } }),
    })
    const { container } = renderAt('/overview', '/overview', <OverviewPage />)
    const dbs = (await screen.findByText('Databases monitored')).parentElement
    expect(dbs && within(dbs).getByText('1')).toBeInTheDocument()
    expect(screen.getByText('All healthy')).toBeInTheDocument()
    expect(screen.getByText('7')).toBeInTheDocument()
    expect(screen.getByText('Max 100')).toBeInTheDocument()
    // No throughput samples: unknown, never zero.
    const qps = screen.getByText('Query throughput').parentElement
    expect(qps && within(qps).getByText('—')).toBeInTheDocument()
    expect(screen.getByText('No replication observed')).toBeInTheDocument()
    expect(await screen.findByText('The SQL editor is off for this instance')).toBeInTheDocument()
    expect(await axeViolations(container)).toEqual([])
  })
})

describe('SqlPage', () => {
  it('runs a statement and draws its plan', async () => {
    const plan = [{ Plan: { 'Node Type': 'Limit', 'Total Cost': 10, 'Plan Rows': 5, Plans: [{ 'Node Type': 'Seq Scan', 'Relation Name': 'orders', Alias: 'orders', 'Total Cost': 8, 'Plan Rows': 100 }] } }]
    const calls = mockFetch({
      '/api/v1/fleet/live': () => ({ body: live }),
      '/api/v1/targets/1/workspace/policy': () => ({ body: sqlPolicy }),
      '/api/v1/targets/1/workspace/run': (init) => {
        const req = JSON.parse(String(init?.body)) as { action: string }
        return req.action === 'explain'
          ? { body: { columns: [{ name: 'QUERY PLAN', type_oid: 114 }], rows: [[JSON.stringify(plan)]], truncated: false, cell_truncated: false, bytes: 10, duration_ms: 1 } }
          : { body: { columns: [{ name: 'id', type_oid: 20 }, { name: 'status', type_oid: 25 }], rows: [['1', 'paid'], ['2', null]], truncated: false, cell_truncated: false, bytes: 20, duration_ms: 3 } }
      },
    })
    const { container } = renderAt('/sql/1', '/sql/:targetId', <SqlPage />)
    const editor = await screen.findByLabelText('SQL statement')
    const user = userEvent.setup()
    await user.clear(editor)
    await user.type(editor, 'SELECT id, status FROM orders LIMIT 5')
    await user.click(screen.getByRole('button', { name: /Run/ }))
    expect(await screen.findByRole('table', { name: 'Statement result' })).toBeInTheDocument()
    expect(screen.getByText('paid')).toBeInTheDocument()
    expect(screen.getByText('NULL')).toBeInTheDocument()
    const run = calls.find((c) => c.url.endsWith('/workspace/run'))
    expect(run && JSON.parse(run.body ?? '{}')).toMatchObject({ action: 'sql', sql: 'SELECT id, status FROM orders LIMIT 5', schema: 'public' })
    await user.click(screen.getByRole('button', { name: /Explain/ }))
    expect(await screen.findByRole('img', { name: /Execution plan: Limit/ })).toBeInTheDocument()
    expect(screen.getByText('on orders')).toBeInTheDocument()
    expect(await axeViolations(container)).toEqual([])
  })
})

describe('roles', () => {
  it('does not offer the SQL editor to a viewer', async () => {
    const calls = mockFetch({
      '/api/v1/fleet/live': () => ({ body: live }),
      '/api/v1/targets/1/workspace/policy': () => ({ body: sqlPolicy }),
    })
    const { container } = renderAt('/sql/1', '/sql/:targetId', <SqlPage />, sessionFor('viewer'))
    expect(await screen.findByText('Not available for your role')).toBeInTheDocument()
    expect(screen.queryByLabelText('SQL statement')).not.toBeInTheDocument()
    expect(calls.some((c) => c.url.includes('/workspace/run'))).toBe(false)
    expect(await axeViolations(container)).toEqual([])
  })

  it('lets an administrator add users but not change the environment administrator', async () => {
    const users = [
      { id: 1, username: 'admin', role: 'admin', disabled: false, managed: true, created_at: '', updated_at: '', last_login_at: null },
      { id: 2, username: 'dana', role: 'viewer', disabled: false, managed: false, created_at: '', updated_at: '', last_login_at: null },
    ]
    const calls = mockFetch({
      '/api/v1/users': (init) => (init?.method === 'POST' ? { status: 201, body: { ...users[1], id: 3, username: 'omar', role: 'operator' } } : { body: { users, roles: ['viewer', 'operator', 'admin'] } }),
      '/api/v1/targets': () => ({ body: { targets: [] } }),
      '/api/v1/settings/rules': () => ({ body: { thresholds: {}, defaults: {} } }),
      '/api/v1/storage': () => ({ body: { tables: [] } }),
    })
    const { container } = renderAt('/settings', '/settings', <SettingsPage />)
    const table = await screen.findByRole('table', { name: 'Users' })
    expect(within(table).getByRole('combobox', { name: 'Role of admin' })).toBeDisabled()
    expect(within(table).getByRole('combobox', { name: 'Role of dana' })).toBeEnabled()
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /Add user/ }))
    const dialog = screen.getByRole('dialog', { name: 'Add user' })
    await user.type(within(dialog).getByLabelText('User name'), 'omar')
    await user.selectOptions(within(dialog).getByLabelText('Role'), 'operator')
    await user.type(within(dialog).getByLabelText(/Initial password/), 'a-long-initial-password')
    await user.click(within(dialog).getByRole('button', { name: 'Add user' }))
    await waitFor(() => expect(calls.find((c) => c.method === 'POST' && c.url === '/api/v1/users')).toBeTruthy())
    const post = calls.find((c) => c.method === 'POST' && c.url === '/api/v1/users')
    expect(JSON.parse(post?.body ?? '{}')).toEqual({ username: 'omar', role: 'operator', password: 'a-long-initial-password' })
    expect(await axeViolations(container)).toEqual([])
  })
})

describe('QuickAddPage', () => {
  it('shows the role statements for a superuser and asks before running them', async () => {
    const plan: SetupPlan = {
      ok: true, error: null, hint: null, mode: 'create_roles', reason: 'This is a superuser.', host: 'db', port: 5432, database: 'shop', user: 'postgres',
      server_version: '16.4', is_standby: false, superuser: true, sees_all_sessions: true, workspace_allowed: true, pg_stat_statements: true,
      suggested_name: 'shop @ db:5432', statements: ["CREATE ROLE locklens_monitor WITH LOGIN PASSWORD '••••••••'", 'GRANT pg_monitor TO locklens_monitor'], existing_roles: [], warnings: [],
    }
    const calls = mockFetch({
      '/api/v1/targets/inspect': () => ({ body: plan }),
      '/api/v1/targets/quick': () => ({ status: 201, body: { target: { ...target, id: 9 }, mode: 'create_roles', warnings: [] } }),
    })
    const { container } = renderAt('/settings/targets/new', '/settings/targets/new', <QuickAddPage />)
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(/Connection string/), 'postgres://postgres:pw@db:5432/shop')
    await user.click(screen.getByRole('button', { name: 'Check connection' }))
    expect(await screen.findByText(/GRANT pg_monitor TO locklens_monitor/)).toBeInTheDocument()
    expect(screen.getByText('superuser')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Create roles and add instance' }))
    expect(calls.some((c) => c.url === '/api/v1/targets/quick')).toBe(false)
    await user.click(within(screen.getByRole('alertdialog', { name: 'Create LockLens roles?' })).getByRole('button', { name: 'Create roles and add' }))
    await waitFor(() => expect(calls.some((c) => c.url === '/api/v1/targets/quick')).toBe(true))
    const body = JSON.parse(calls.find((c) => c.url === '/api/v1/targets/quick')?.body ?? '{}')
    expect(body).toMatchObject({ dsn: 'postgres://postgres:pw@db:5432/shop', confirm: true, browse_data: true, run_sql: true })
    expect(await axeViolations(container)).toEqual([])
  })
})


describe('LoadPage', () => {
  it('shows load by wait class and leads to the statements waiting on it', async () => {
    const byClass: LoadResponse = {
      from: '2026-10-24T12:00:00Z',
      to: '2026-10-24T12:15:00Z',
      step_seconds: 30,
      load: {
        dimension: 'class',
        ticks: 450,
        active_aas: 3.5,
        idle_in_transaction_aas: 1,
        series: [{ key: 'Lock', label: 'Lock', points: [{ t: '2026-10-24T12:00:00Z', v: 3 }, { t: '2026-10-24T12:00:30Z', v: null }] }],
        top: [
          { key: 'Lock', values: [], label: 'Lock', aas: 3, share: 0.67, classes: { Lock: 3 } },
          { key: 'IdleTx', values: [], label: 'Idle in transaction', aas: 1, share: 0.22, classes: { IdleTx: 1 } },
        ],
      },
      statements: [],
      query_text_enabled: true,
      collector: { name: 'load', interval_seconds: 2, enabled: true, last_attempt: null, last_success: null, last_error: null, last_duration_ms: null, consecutive_failures: 0 },
    }
    const byQuery: LoadResponse = {
      ...byClass,
      load: { ...byClass.load, dimension: 'query', series: [], top: [{ key: '5/10/-42', values: ['5', '10', '-42', 'shop', 'web'], label: 'queryid -42', aas: 3, share: 1, classes: { Lock: 3 } }] },
      statements: [{ key: { server_major: 18, dbid: 5, userid: 10, toplevel: true, queryid: '-42' }, identity_id: 9, datname: 'shop', username: 'web', command: 'update', query_text: 'UPDATE stock SET qty = qty - 1 WHERE id = $1' }],
    }
    const calls = mockFetch({ '/api/v1/targets/1/load': (_init, url) => ({ body: url.searchParams.get('by') === 'query' ? byQuery : byClass }) })
    const { container } = renderAt('/databases/1/load', '/databases/:id/load', <LoadPage />)
    expect(await screen.findByText(/^3\.50?$/)).toBeInTheDocument()
    const table = screen.getByRole('table', { name: /wait class groups/ })
    expect(within(table).getByText('Lock')).toBeInTheDocument()
    expect(await axeViolations(container)).toEqual([])
    const drill = within(table).getAllByRole('button', { name: /Statements/ })[0]
    if (!drill) throw new Error('no drill-down button')
    await userEvent.setup().click(drill)
    await waitFor(() => expect(calls.some((c) => c.url.includes('by=query') && c.url.includes('class=Lock'))).toBe(true))
    expect(await screen.findByTitle('UPDATE stock SET qty = qty - 1 WHERE id = $1')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /UPDATE stock/ })).toHaveAttribute('href', '/queries/1/9')
    expect(screen.getByRole('button', { name: 'Remove the Lock filter' })).toBeInTheDocument()
  })
})

describe('NotificationsCard', () => {
  it('adds a channel with its destination and edits it without resending the destination', async () => {
    const channel = { id: 4, name: '#db-oncall', kind: 'slack', enabled: true, min_severity: 'warning', environments: [], on_open: true, on_escalate: true, on_resolve: true, reminder_seconds: 0, display: 'https://hooks.slack.com', created_at: '', updated_at: '' }
    const calls = mockFetch({
      '/api/v1/notifications/channels': (init) => ({ status: init?.method === 'POST' ? 201 : 200, body: init?.method && init.method !== 'GET' ? channel : { channels: [channel] } }),
      '/api/v1/notifications/deliveries': () => ({ body: { deliveries: [] } }),
    })
    const user = userEvent.setup()
    const { container } = renderAt('/settings', '/settings', <NotificationsCard />)
    expect(await screen.findByText('https://hooks.slack.com')).toBeInTheDocument()
    expect(await axeViolations(container)).toEqual([])

    await user.click(screen.getByRole('button', { name: /Add channel/ }))
    const add = screen.getByRole('dialog', { name: 'Add a notification channel' })
    await user.type(within(add).getByLabelText('Name'), '#alerts')
    await user.type(within(add).getByLabelText(/Incoming webhook URL/), 'https://hooks.slack.com/services/T/B/token')
    await user.click(within(add).getByRole('button', { name: 'Add channel' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'POST')).toBe(true))
    const created = JSON.parse(calls.find((c) => c.method === 'POST')?.body ?? '{}')
    expect(created).toMatchObject({ name: '#alerts', kind: 'slack', destination: { url: 'https://hooks.slack.com/services/T/B/token' } })

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: 'Edit' }))
    const edit = screen.getByRole('dialog', { name: 'Edit notification channel' })
    expect(within(edit).queryByLabelText(/Incoming webhook URL/)).not.toBeInTheDocument()
    await user.click(within(edit).getByRole('button', { name: 'Save channel' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true))
    const updated = JSON.parse(calls.find((c) => c.method === 'PUT')?.body ?? '{}')
    expect(updated.destination).toBeUndefined()
    expect(calls.find((c) => c.method === 'PUT')?.url).toBe('/api/v1/notifications/channels/4')
  })
})
