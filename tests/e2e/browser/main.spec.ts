import { spawn, spawnSync, type ChildProcess } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { pathToFileURL } from 'node:url'
import { expect, test, type Page } from '@playwright/test'

// The main path through the product against a real stack: sign in, see the
// whole environment, see what a database is doing, watch queries by type
// during real traffic, browse tables and rows, run read-only SQL, follow a
// blocking chain to its incident, export the report, and open it offline.
// Nothing here mocks or intercepts the API.

const project = env('LOCKLENS_E2E_COMPOSE_PROJECT')
// Compose files (comma-separated) and the lab's env file.
const composeFiles = env('LOCKLENS_E2E_COMPOSE_FILES').split(',')
const composeEnvFile = env('LOCKLENS_E2E_COMPOSE_ENV_FILE')
const adminPassword = env('LOCKLENS_E2E_ADMIN_PASSWORD')
// The lab's fake observer credentials, as configured in demo/postgres/01-roles.sql.
const targetDSN = 'postgres://locklens_observer:observer-pw@primary:5432/locklens_demo?sslmode=disable'
// The lab's fake superuser (deploy/compose.lab.yaml), used once to let LockLens create its roles.
const superuserDSN = 'postgres://postgres:lab-postgres-pw@primary:5432/locklens_demo?sslmode=disable'
const forbidden = ['observer-pw', 'reader-pw', 'lab-store-pw', 'lab-postgres-pw', 'demo-app-pw', adminPassword, 'postgres://']

function env(name: string): string {
  const v = process.env[name]
  if (!v) throw new Error(`${name} is not set; run ./scripts/e2e.sh`)
  return v
}

function compose(...args: string[]): string[] {
  return ['compose', '-p', project, ...composeFiles.flatMap((f) => ['-f', f]), '--env-file', composeEnvFile, ...args]
}

const require = createRequire(import.meta.url)
const axePath = require.resolve('axe-core/axe.min.js')

type AxeViolation = { id: string; impact: string | null; help: string; nodes: { target: string[] }[] }

// expectAccessible runs axe-core on the current page and fails on serious or
// critical violations.
async function expectAccessible(page: Page, what: string) {
  await page.addScriptTag({ path: axePath })
  const violations = await page.evaluate(async () => {
    const axe = (window as unknown as { axe: { run: (ctx: Document, opts: object) => Promise<{ violations: AxeViolation[] }> } }).axe
    const r = await axe.run(document, { resultTypes: ['violations'] })
    return r.violations
      .filter((v) => v.impact === 'serious' || v.impact === 'critical')
      .map((v) => ({ id: v.id, impact: v.impact, help: v.help, nodes: v.nodes.slice(0, 3).map((n) => ({ target: n.target })) }))
  })
  expect(violations, `accessibility violations on ${what}`).toEqual([])
}

// Console errors and failed requests, except the expected 401 of the session
// probe before signing in and the deliberate failed login.
function watchConsole(page: Page): string[] {
  const errors: string[] = []
  page.on('console', (m) => {
    if (m.type() === 'error' && !/^Failed to load resource/.test(m.text())) errors.push(m.text())
  })
  page.on('pageerror', (e) => errors.push(String(e)))
  page.on('response', (r) => {
    const path = new URL(r.url()).pathname
    if (r.status() < 400 || (r.status() === 401 && ['/api/v1/session', '/api/v1/auth/login'].includes(path))) return
    errors.push(`${r.request().method()} ${r.url()} -> ${r.status()}`)
  })
  return errors
}

test.describe.configure({ mode: 'serial' })

const scenarios: ChildProcess[] = []

function startScenario(name: string, duration: string) {
  scenarios.push(spawn('docker', compose('run', '--rm', 'lab', 'run', name, '--duration', duration), { stdio: 'ignore' }))
}

test.afterAll(() => {
  for (const s of scenarios) s.kill('SIGINT')
  spawnSync('docker', compose('run', '--rm', 'lab', 'stop'), { stdio: 'ignore', timeout: 60_000 })
})

test('main investigation path', async ({ page, browser }, testInfo) => {
  const consoleErrors = watchConsole(page)

  await test.step('sign in and land on the fleet overview', async () => {
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Sign in to LockLens' })).toBeVisible()
    await page.getByLabel('Username').fill('admin')
    await page.getByLabel('Password').fill('not-the-password')
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByRole('alert')).toBeVisible()
    await expectAccessible(page, 'login')
    await page.getByLabel('Password').fill(adminPassword)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page).toHaveURL(/\/overview$/)
    await expect(page.getByRole('heading', { name: 'Overview', exact: true })).toBeVisible()
    // The session token lives in an HttpOnly cookie, never in web storage.
    const stored = await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }))
    expect(stored).not.toMatch(/csrf|session|token/i)
  })

  await test.step('overview shows the whole environment', async () => {
    const main = page.getByRole('main')
    for (const kpi of ['Databases monitored', 'Active connections', 'Query throughput', 'Replication health', 'Slow queries', 'Incidents']) {
      await expect(main.getByText(kpi, { exact: true }).first()).toBeVisible()
    }
    await expect(main.getByText('All healthy')).toBeVisible({ timeout: 60_000 })
    for (const card of ['Read / write operations', 'Query throughput & latency', 'System resources', 'Database topology', 'Top queries', 'Active sessions', 'Blocking transactions', 'Database explorer', 'SQL editor']) {
      await expect(page.getByRole('heading', { name: card, exact: true })).toBeVisible()
    }
    // Replication is observed, not configured: the standby appears under its primary.
    await expect(page.getByRole('list', { name: 'Standbys of lab-primary' }).getByText('lab-standby', { exact: true })).toBeVisible({ timeout: 90_000 })
    await expectAccessible(page, 'overview')
  })

  let base = ''
  let targetId = ''
  await test.step('an instance shows what its database is doing', async () => {
    await page.getByRole('link', { name: 'Databases', exact: true }).click()
    await page.getByRole('link', { name: /lab-primary/ }).first().click()
    await expect(page).toHaveURL(/\/databases\/\d+$/)
    base = new URL(page.url()).pathname
    targetId = base.split('/').pop() ?? ''
    await expect(page.getByRole('heading', { name: /lab-primary/ })).toBeVisible()
    for (const kpi of ['Connections', 'Running now', 'Queries per second', 'Transactions per second', 'Cache hit ratio', 'Database size']) {
      await expect(page.getByText(kpi, { exact: true }).first()).toBeVisible()
    }
    await expect(page.getByRole('heading', { name: 'Queries by type' })).toBeVisible()
    await expectAccessible(page, 'instance overview')
  })

  await test.step('queries by type during real traffic', async () => {
    startScenario('shop', '4m')
    await page.goto(`${base}/queries`)
    await expect(page.getByRole('heading', { name: 'Queries', exact: true })).toBeVisible()
    const breakdown = page.getByRole('table', { name: 'Workload by statement type' })
    // Statements are sampled every 15 s in demo mode; deltas need two samples.
    await expect(async () => {
      await page.reload()
      for (const type of ['SELECT', 'INSERT', 'UPDATE', 'DELETE']) await expect(breakdown.getByText(type, { exact: true })).toBeVisible({ timeout: 3_000 })
    }).toPass({ timeout: 150_000 })
    await expect(page.getByRole('figure', { name: /by statement type/ })).toBeVisible()
    const group = page.getByRole('group', { name: 'Statement type' })
    await group.getByRole('button', { name: 'UPDATE' }).click()
    await expect(page).toHaveURL(/command=update/)
    // The stack's background workload also updates its own table; wait for
    // the shop scenario's statement and pick it.
    const first = page.getByRole('table', { name: 'Statements' }).getByRole('link').filter({ hasText: 'shop.orders' }).first()
    await expect(async () => {
      await page.goto(`${base}/queries?command=update&q=shop.orders`)
      await expect(first).toBeVisible({ timeout: 3_000 })
    }).toPass({ timeout: 90_000 })
    await expect(first).toContainText('UPDATE')
    await expectAccessible(page, 'queries')
    await first.click()
    await expect(page).toHaveURL(/\/queries\/\d+\/\d+$/)
    await expect(page.getByRole('heading', { name: /UPDATE statement/ })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'SQL' })).toBeVisible()
    await expect(page.locator('code').filter({ hasText: 'shop.orders' }).first()).toBeVisible()
    await expectAccessible(page, 'query detail')

    // The same statements ranked across instances.
    await page.goto('/queries?command=update')
    await expect(page.getByRole('table', { name: 'Queries across instances' }).locator('code').filter({ hasText: 'shop.orders' }).first()).toBeVisible()
    await expectAccessible(page, 'queries across instances')
  })

  await test.step('browse tables and rows', async () => {
    await page.goto(`/explorer/${targetId}/shop`)
    await expect(page.getByRole('heading', { name: 'Explorer', exact: true })).toBeVisible()
    await page.getByRole('navigation', { name: 'Schema objects' }).getByRole('button', { name: /^orders/ }).click()
    await expect(page).toHaveURL(/explorer\/\d+\/shop\/orders/)
    const grid = page.getByRole('table', { name: 'Rows of shop.orders' })
    await expect(grid).toBeVisible()
    await expect(grid.getByRole('columnheader', { name: /customer_id/ })).toBeVisible()
    // Filter: status equals 'paid'.
    await page.getByLabel('Filter column').selectOption('status')
    await page.getByLabel('Condition').selectOption('eq')
    await page.getByLabel('Value').fill('paid')
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect(async () => {
      const cells = await grid.locator('tbody tr td:nth-child(3)').allInnerTexts()
      expect(cells.length).toBeGreaterThan(0)
      expect(new Set(cells)).toEqual(new Set(['paid']))
    }).toPass({ timeout: 20_000 })
    await grid.locator('tbody tr').first().locator('td').first().getByRole('button').click()
    await expect(page.getByRole('dialog')).toBeVisible()
    await page.keyboard.press('Escape')
    await page.getByRole('tab', { name: /Schema/ }).click()
    await expect(page.getByText('customer_id').first()).toBeVisible()
    await page.getByRole('tab', { name: /Triggers/ }).click()
    await expect(page.getByRole('cell', { name: 'orders_touch_updated_at', exact: true })).toBeVisible()
    await page.getByRole('button', { name: /^Functions/ }).click()
    await expect(page.getByRole('button', { name: /customer_order_count/ })).toBeVisible()
    await expectAccessible(page, 'explorer')
  })

  await test.step('read-only SQL editor: results, plan, and a refused write', async () => {
    await page.goto(`/sql/${targetId}`)
    const editor = page.getByLabel('SQL statement')
    await editor.fill("SELECT o.id, o.status, c.full_name FROM shop.orders o JOIN shop.customers c ON c.id = o.customer_id WHERE o.status = 'paid' ORDER BY o.id LIMIT 10")
    await page.getByRole('button', { name: /Run/ }).click()
    const result = page.getByRole('table', { name: 'Statement result' })
    await expect(result).toBeVisible()
    await expect(result.locator('tbody tr')).toHaveCount(10)
    await page.getByRole('button', { name: /Explain/ }).click()
    await expect(page.getByRole('img', { name: /Execution plan: Limit/ })).toBeVisible()
    await expectAccessible(page, 'sql editor')
    // The statement runs in a read-only transaction that is rolled back.
    await editor.fill("UPDATE shop.orders SET note = 'e2e' WHERE id = 1")
    await page.getByRole('button', { name: /Run/ }).click()
    await expect(page.getByText('PostgreSQL did not run the statement')).toBeVisible()
    // That refusal is the one expected failed request of this test.
    const refused = consoleErrors.findIndex((e) => /POST .*\/workspace\/run -> 400$/.test(e))
    expect(refused, 'the refused write answers 400').toBeGreaterThanOrEqual(0)
    consoleErrors.splice(refused, 1)
    const note = await page.request.post(`/api/v1/targets/${targetId}/workspace/run`, {
      headers: { 'X-CSRF-Token': await page.evaluate(() => fetch('/api/v1/session').then((r) => r.json()).then((s: { csrf_token: string }) => s.csrf_token)) },
      data: { request_id: crypto.randomUUID(), action: 'data', data: { schema: 'shop', relation: 'orders', filter_column: 'note', filter_op: 'eq', filter_value: 'e2e', limit: 1, offset: 0 } },
    })
    expect((await note.json()).rows).toEqual([])
  })

  let incidentURL = ''
  await test.step('a blocking chain: live sessions, tree, and incident', async () => {
    startScenario('chain', '4m')
    await page.goto('/blocking')
    await expect(page.getByRole('list', { name: /Sessions waiting on/ }).first()).toBeVisible({ timeout: 90_000 })
    await expectAccessible(page, 'blocking')
    await page.goto('/sessions')
    await page.getByRole('table', { name: 'Sessions' }).locator('tbody tr').first().click()
    await expect(page.getByRole('dialog')).toBeVisible()
    await page.keyboard.press('Escape')
    await expectAccessible(page, 'sessions')
    await page.goto(`${base}/activity`)
    await expect(page.getByRole('heading', { name: 'Live activity' })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Who is blocking whom' })).toBeVisible({ timeout: 90_000 })
    await expect(page.getByRole('list', { name: /Blocking tree/ }).first()).toBeVisible()
    await page.getByRole('table', { name: 'Sessions' }).locator('tbody tr').first().click()
    await expect(page.getByRole('dialog')).toBeVisible()
    await expect(page.getByRole('dialog').getByText(/Current query|Last query/)).toBeVisible()
    await page.keyboard.press('Escape')
    await expectAccessible(page, 'activity')

    await page.goto(`${base}/alerts`)
    const link = page.getByRole('link', { name: /Blocking chain/ }).first()
    await expect(async () => {
      if (!(await link.isVisible())) await page.reload()
      await expect(link).toBeVisible({ timeout: 5_000 })
    }).toPass({ timeout: 120_000 })
    await expectAccessible(page, 'alerts')
    await link.click()
    await expect(page).toHaveURL(/\/incidents\/\d+/)
    incidentURL = new URL(page.url()).pathname
    await expect(page.getByText('trigger', { exact: true }).first()).toBeVisible()
    await page.getByRole('button', { name: 'Show blocking graph' }).first().click()
    await expect(page.getByRole('list', { name: /Blocking tree/ }).first()).toBeVisible()
    await expectAccessible(page, 'incident')
  })

  const reportPath = testInfo.outputPath('incident-report.html')
  await test.step('export the report and open it offline', async () => {
    const download = page.waitForEvent('download')
    await page.getByRole('link', { name: 'Export HTML report' }).click()
    await (await download).saveAs(reportPath)
    const html = readFileSync(reportPath, 'utf8')
    expect(html).toContain('Blocking graphs')
    expect(html).not.toMatch(/<script/i)
    for (const s of forbidden) expect(html, 'report must not contain credentials').not.toContain(s)
    const ctx = await browser.newContext({ offline: true, javaScriptEnabled: false })
    const offline = await ctx.newPage()
    const requests: string[] = []
    offline.on('request', (r) => requests.push(r.url()))
    const url = pathToFileURL(reportPath).href
    await offline.goto(url)
    await expect(offline.getByRole('heading', { name: 'Blocking graphs' })).toBeVisible()
    expect(requests.filter((r) => r !== url), 'the report must not load anything').toEqual([])
    await ctx.close()
  })

  await test.step('export JSON and import it again', async () => {
    await page.goto(incidentURL)
    const download = page.waitForEvent('download')
    await page.getByRole('link', { name: 'Export JSON' }).click()
    const jsonPath = testInfo.outputPath('incident.json')
    await (await download).saveAs(jsonPath)
    const json = readFileSync(jsonPath, 'utf8')
    expect(JSON.parse(json).format).toBe('locklens-incident')
    for (const s of forbidden) expect(json, 'export must not contain credentials').not.toContain(s)
    await page.goto('/settings')
    await page.getByLabel('Incident JSON file').setInputFiles(jsonPath)
    await expect(page).toHaveURL(/\/incidents\/\d+/)
    expect(new URL(page.url()).pathname).not.toBe(incidentURL)
    await expect(page.getByText('import', { exact: true })).toBeVisible()
  })

  await test.step('add an instance from one superuser connection string', async () => {
    await page.goto('/settings/targets/new')
    await page.getByLabel(/Connection string/).fill(superuserDSN)
    await page.getByLabel('Name (optional)').fill('e2e-primary')
    await page.getByRole('main').getByLabel('Environment').fill('lab')
    await page.getByRole('button', { name: 'Check connection' }).click()
    // LockLens shows the statements it will run and never the password.
    await expect(page.getByText(/will run these statements once/)).toBeVisible()
    await expect(page.locator('pre').filter({ hasText: 'CREATE ROLE "locklens_monitor"' })).toBeVisible()
    await expect(page.locator('pre')).toContainText('GRANT pg_read_all_data TO "locklens_reader"')
    // The typed input holds the password; nothing the server returned does.
    expect(await page.getByRole('main').innerText()).not.toContain('lab-postgres-pw')
    await expectAccessible(page, 'add instance')
    await page.getByRole('button', { name: 'Create roles and add instance' }).click()
    await page.getByRole('alertdialog', { name: 'Create LockLens roles?' }).getByRole('button', { name: 'Create roles and add' }).click()
    await expect(page).toHaveURL(/\/databases\/\d+$/, { timeout: 30_000 })
    await expect(page.getByRole('heading', { name: /e2e-primary/ })).toBeVisible()
    await expect(page.getByRole('main').getByText('healthy', { exact: true }).first()).toBeVisible({ timeout: 60_000 })
    // Only the generated monitoring role is stored, not the superuser.
    await page.goto('/settings')
    await expect(page.getByRole('row').filter({ hasText: 'e2e-primary' })).toContainText('locklens_monitor@primary')
    // The advanced form still tests a connection without saving it.
    await page.goto('/settings/targets/new/manual')
    await page.getByLabel('Connection string (DSN)').fill(targetDSN)
    await page.getByRole('button', { name: 'Test connection' }).click()
    const result = page.getByRole('status').filter({ hasText: 'Connection' })
    await expect(result).toContainText('Connection works.')
    await expect(result).not.toContainText('observer-pw')
  })

  await test.step('roles: a viewer reads but cannot run SQL', async () => {
    await page.goto('/settings')
    await page.getByRole('button', { name: /Add user/ }).click()
    const dialog = page.getByRole('dialog', { name: 'Add user' })
    await dialog.getByLabel('User name').fill('e2e-viewer')
    await dialog.getByLabel('Role').selectOption('viewer')
    await dialog.getByLabel(/Initial password/).fill('e2e-viewer-password')
    await dialog.getByRole('button', { name: 'Add user' }).click()
    await expect(page.getByRole('table', { name: 'Users' }).getByRole('cell', { name: 'e2e-viewer', exact: true })).toBeVisible()
    await expectAccessible(page, 'users')

    const ctx = await browser.newContext()
    const viewer = await ctx.newPage()
    const viewerErrors = watchConsole(viewer)
    await viewer.goto('/login')
    await viewer.getByLabel('Username').fill('e2e-viewer')
    await viewer.getByLabel('Password').fill('e2e-viewer-password')
    await viewer.getByRole('button', { name: 'Sign in' }).click()
    await expect(viewer.getByRole('heading', { name: 'Overview', exact: true })).toBeVisible()
    const nav = viewer.getByRole('navigation', { name: 'Main' })
    await expect(nav.getByRole('link', { name: 'Blocking' })).toBeVisible()
    await expect(nav.getByRole('link', { name: 'SQL Editor' })).toHaveCount(0)
    await expect(nav.getByRole('link', { name: 'Settings' })).toHaveCount(0)
    await viewer.goto('/sql')
    await expect(viewer.getByRole('heading', { name: 'Not available for your role' })).toBeVisible()
    const csrf = await viewer.evaluate(() => fetch('/api/v1/session').then((r) => r.json()).then((s: { csrf_token: string }) => s.csrf_token))
    const denied = await viewer.request.post(`/api/v1/targets/${targetId}/workspace/run`, { headers: { 'X-CSRF-Token': csrf }, data: { request_id: 'e2e-viewer-sql', action: 'sql', sql: 'SELECT 1', schema: 'public' } })
    expect(denied.status()).toBe(403)
    expect((await viewer.request.get('/api/v1/users')).status()).toBe(403)
    expect(viewerErrors).toEqual([])
    await ctx.close()
  })

  await test.step('remaining pages', async () => {
    await page.goto('/databases')
    await expect(page.getByRole('heading', { name: 'lab-standby', exact: true })).toBeVisible()
    await expectAccessible(page, 'databases')
    // Exact names: the lab can have more standbys (lab-standby-2, …).
    await page.getByRole('link').filter({ has: page.getByRole('heading', { name: 'lab-standby', exact: true }) }).click()
    await page.getByRole('link', { name: 'Replication', exact: true }).click()
    await expect(page.getByText('streaming').first()).toBeVisible({ timeout: 60_000 })
    await expectAccessible(page, 'standby replication')
    await page.goto('/topology')
    await expect(page.getByRole('table').filter({ hasText: 'lab-standby' }).getByText('streaming').first()).toBeVisible()
    await expectAccessible(page, 'topology')
    await page.goto('/connections')
    await expect(page.getByRole('table', { name: /Connections grouped/ })).toContainText('demo_app')
    await expectAccessible(page, 'connections')
    await page.goto('/incidents')
    await page.getByRole('radio', { name: 'All' }).click()
    await expect(page.getByRole('link', { name: /Blocking chain/ }).first()).toBeVisible()
    await expectAccessible(page, 'incidents')
    await page.goto('/settings')
    await expect(page.getByRole('table', { name: 'Monitoring store tables' })).toContainText('metric_points')
    await expectAccessible(page, 'settings')
    await page.goto(incidentURL)
    await page.getByRole('button', { name: /account menu/i }).click()
    await page.getByRole('menuitem', { name: /Switch to (dark|light) theme/ }).click()
    // Let the 150 ms color transitions settle before measuring contrast.
    await page.waitForTimeout(500)
    await expectAccessible(page, 'incident, other theme')
  })

  await test.step('sign out', async () => {
    // The menu stays open after switching the theme.
    const logout = page.getByRole('menuitem', { name: /Log out/ })
    if (!(await logout.isVisible())) await page.getByRole('button', { name: /account menu/i }).click()
    await logout.click()
    await expect(page.getByRole('heading', { name: 'Sign in to LockLens' })).toBeVisible()
    expect((await page.request.get('/api/v1/targets')).status()).toBe(401)
  })

  expect(consoleErrors, 'browser console errors').toEqual([])
})
