import { spawn, spawnSync, type ChildProcess } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { pathToFileURL } from 'node:url'
import { expect, test, type Page } from '@playwright/test'

// The main path through the product against a real stack: sign in, see what
// a database is doing, watch queries by type during real traffic, browse
// tables and rows, follow a blocking chain to its incident, export the
// report, and open it offline. Nothing here mocks or intercepts the API.

const project = env('LOCKLENS_E2E_COMPOSE_PROJECT')
const composeFile = env('LOCKLENS_E2E_COMPOSE_FILE')
const adminPassword = env('LOCKLENS_E2E_ADMIN_PASSWORD')
// The lab's fake observer credentials, as configured in demo/postgres/01-roles.sql.
const targetDSN = 'postgres://locklens_observer:observer-pw@primary:5432/locklens_demo?sslmode=disable'
const forbidden = ['observer-pw', 'reader-pw', 'lab-store-pw', 'lab-postgres-pw', 'demo-app-pw', adminPassword, 'postgres://']

function env(name: string): string {
  const v = process.env[name]
  if (!v) throw new Error(`${name} is not set; run ./scripts/e2e.sh`)
  return v
}

function compose(...args: string[]): string[] {
  return ['compose', '-p', project, '-f', composeFile, ...args]
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

  await test.step('sign in and land on the instance overview', async () => {
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Sign in to LockLens' })).toBeVisible()
    await page.getByLabel('Password').fill('not-the-password')
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByRole('alert')).toBeVisible()
    await expectAccessible(page, 'login')
    await page.getByLabel('Password').fill(adminPassword)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page).toHaveURL(/\/i\/\d+\/overview/)
    await expect(page.getByRole('heading', { name: /lab-primary/ })).toBeVisible()
    // The session token lives in an HttpOnly cookie, never in web storage.
    const stored = await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }))
    expect(stored).not.toMatch(/csrf|session|token/i)
  })

  let base = ''
  await test.step('overview shows what the database is doing', async () => {
    base = new URL(page.url()).pathname.replace(/\/overview$/, '')
    await expect(page.getByRole('main').getByText('healthy', { exact: true }).first()).toBeVisible({ timeout: 60_000 })
    for (const kpi of ['Connections', 'Running now', 'Queries per second', 'Transactions per second', 'Cache hit ratio', 'Database size']) {
      await expect(page.getByText(kpi, { exact: true }).first()).toBeVisible()
    }
    await expect(page.getByRole('heading', { name: 'Queries by type' })).toBeVisible()
    await expectAccessible(page, 'overview')
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
    const first = page.getByRole('table', { name: 'Statements' }).getByRole('link').first()
    await expect(first).toContainText('UPDATE')
    await expectAccessible(page, 'queries')
    await first.click()
    await expect(page.getByRole('heading', { name: /UPDATE statement/ })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'SQL' })).toBeVisible()
    await expect(page.locator('code').filter({ hasText: 'shop.orders' }).first()).toBeVisible()
    await expectAccessible(page, 'query detail')
  })

  await test.step('browse tables and rows', async () => {
    await page.goto(`${base}/explorer/shop`)
    await expect(page.getByRole('heading', { name: 'Data explorer' })).toBeVisible()
    await page.getByRole('link', { name: /^orders/ }).first().click()
    await expect(page).toHaveURL(/explorer\/shop\/orders/)
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
    await page.getByRole('tab', { name: /Columns/ }).click()
    await expect(page.getByText('customer_id').first()).toBeVisible()
    await expectAccessible(page, 'explorer')
  })

  let incidentURL = ''
  await test.step('a blocking chain: live sessions, tree, and incident', async () => {
    startScenario('chain', '4m')
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

  await test.step('add and test an instance', async () => {
    await page.goto('/settings/targets/new')
    await page.getByLabel('Name', { exact: true }).fill('e2e-primary')
    await page.getByLabel('Connection string (DSN)').fill(targetDSN)
    await page.getByRole('button', { name: 'Test connection' }).click()
    const result = page.getByRole('status').filter({ hasText: 'Connection' })
    await expect(result).toContainText('Connection works.')
    await expect(result).toContainText('query statistics available')
    await expect(result).not.toContainText('observer-pw')
    await expect(page.getByRole('checkbox', { name: /Collect SQL text/ })).toBeChecked()
    await expectAccessible(page, 'add instance')
    await page.getByRole('button', { name: 'Add instance' }).click()
    await expect(page).toHaveURL(/\/i\/\d+\/overview/)
    await expect(page.getByRole('heading', { name: /e2e-primary/ })).toBeVisible()
    await expect(page.getByRole('main').getByText('healthy', { exact: true }).first()).toBeVisible({ timeout: 60_000 })
  })

  await test.step('remaining pages', async () => {
    await page.goto('/instances')
    await expect(page.getByRole('heading', { name: 'lab-standby' })).toBeVisible()
    await expectAccessible(page, 'instances')
    await page.getByRole('link', { name: /lab-standby/ }).click()
    await page.goto(new URL(page.url()).pathname.replace(/overview$/, 'replication'))
    await expect(page.getByText('streaming').first()).toBeVisible({ timeout: 60_000 })
    await expectAccessible(page, 'standby replication')
    await page.goto('/settings')
    await expect(page.getByRole('table', { name: 'Monitoring store tables' })).toContainText('metric_points')
    await expectAccessible(page, 'settings')
    await page.goto(incidentURL)
    await page.getByRole('button', { name: /Switch to (dark|light) theme/ }).click()
    // Let the 150 ms color transitions settle before measuring contrast.
    await page.waitForTimeout(500)
    await expectAccessible(page, 'incident, other theme')
  })

  await test.step('sign out', async () => {
    await page.getByRole('button', { name: /Log out/ }).click()
    await expect(page.getByRole('heading', { name: 'Sign in to LockLens' })).toBeVisible()
    expect((await page.request.get('/api/v1/targets')).status()).toBe(401)
  })

  expect(consoleErrors, 'browser console errors').toEqual([])
})
