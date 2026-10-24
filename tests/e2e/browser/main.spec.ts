import { spawn, spawnSync, type ChildProcess } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { pathToFileURL } from 'node:url'
import { expect, test, type Page } from '@playwright/test'

// The main path through the product against a real stack: sign in, add and
// test a target, watch live data, start a lab scenario, find the incident,
// open its evidence, export the HTML report, and open the report offline.
// Nothing here mocks or intercepts the API.

const project = env('LOCKLENS_E2E_COMPOSE_PROJECT')
const composeFile = env('LOCKLENS_E2E_COMPOSE_FILE')
const adminPassword = env('LOCKLENS_E2E_ADMIN_PASSWORD')
// The lab's fake observer credentials, as configured in demo/postgres/01-roles.sql.
const targetDSN = 'postgres://locklens_observer:observer-pw@primary:5432/locklens_demo?sslmode=disable'
const forbidden = ['observer-pw', 'lab-store-pw', 'lab-postgres-pw', 'demo-app-pw', adminPassword, 'postgres://']

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
    // Failed requests are reported with their URL below.
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

let scenario: ChildProcess | undefined

test.afterAll(() => {
  scenario?.kill('SIGINT')
  spawnSync('docker', compose('run', '--rm', 'lab', 'stop'), { stdio: 'ignore', timeout: 60_000 })
})

test('main investigation path', async ({ page, browser }, testInfo) => {
  const consoleErrors = watchConsole(page)

  await test.step('sign in', async () => {
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Sign in to LockLens' })).toBeVisible()
    await page.getByLabel('Username').fill('admin')
    await page.getByLabel('Password').fill('not-the-password')
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByRole('alert')).toBeVisible()
    await expectAccessible(page, 'login')
    await page.getByLabel('Password').fill(adminPassword)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByRole('heading', { name: 'Fleet overview' })).toBeVisible()
    // The session token lives in an HttpOnly cookie, never in web storage.
    const stored = await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }))
    expect(stored).not.toMatch(/csrf|session|token/i)
  })

  await test.step('fleet shows the bootstrapped lab targets', async () => {
    await expect(page.getByRole('heading', { name: 'lab-primary' })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'lab-standby' })).toBeVisible()
    await expect(page.getByText('healthy').first()).toBeVisible({ timeout: 60_000 })
    await expectAccessible(page, 'fleet')
  })

  let targetURL = ''
  await test.step('add and test a target', async () => {
    await page.goto('/settings')
    await page.getByRole('link', { name: 'Add target' }).click()
    await expect(page.getByRole('heading', { name: 'Add target' })).toBeVisible()
    await page.getByLabel('Name', { exact: true }).fill('e2e-primary')
    await page.getByLabel('Description').fill('Added by the browser E2E test')
    await page.getByLabel('Connection string (DSN)').fill(targetDSN)
    await page.getByRole('button', { name: 'Test connection' }).click()
    const result = page.getByRole('status').filter({ hasText: 'Connection' })
    await expect(result).toContainText('Connection works.')
    await expect(result).toContainText('pg_stat_statements available')
    await expect(result).not.toContainText('observer-pw')
    await expectAccessible(page, 'add target')
    await page.getByRole('button', { name: 'Add target' }).click()
    await expect(page).toHaveURL(/\/targets\/\d+/)
    targetURL = new URL(page.url()).pathname
  })

  await test.step('target overview shows live data', async () => {
    await expect(page.getByRole('heading', { name: /e2e-primary/ })).toBeVisible()
    await expect(page.getByText('healthy').first()).toBeVisible({ timeout: 60_000 })
    await expect(page.getByText('PostgreSQL 18.6', { exact: false }).first()).toBeVisible()
    // Charts render from stored history once a few samples exist.
    await expect(page.getByText('Sessions by state')).toBeVisible()
    await expect(page.locator('.recharts-surface').first()).toBeVisible({ timeout: 60_000 })
    await expectAccessible(page, 'target overview')
  })

  await test.step('start the blocking-chain lab scenario', async () => {
    scenario = spawn('docker', compose('run', '--rm', 'lab', 'run', 'chain', '--duration', '4m'), { stdio: 'ignore' })
    await page.goto(`${targetURL}/blocking`)
    await expect(page.getByRole('heading', { name: 'Blocking explorer' })).toBeVisible()
    await expect(page.getByRole('list', { name: /Blocking tree/ }).first()).toBeVisible({ timeout: 90_000 })
    await expectAccessible(page, 'blocking explorer')
  })

  let incidentURL = ''
  await test.step('find the recorded incident', async () => {
    await page.goto('/incidents')
    await expect(page.getByRole('heading', { name: 'Incidents' })).toBeVisible()
    const row = page.getByRole('row').filter({ hasText: 'e2e-primary' }).filter({ hasText: 'blocking.' }).first()
    // The list refreshes through server-sent events; reload only as a fallback.
    await expect(async () => {
      if (!(await row.isVisible())) await page.reload()
      await expect(row).toBeVisible({ timeout: 5_000 })
    }).toPass({ timeout: 120_000 })
    await expectAccessible(page, 'incidents')
    await row.getByRole('link').click()
    await expect(page).toHaveURL(/\/incidents\/\d+/)
    incidentURL = new URL(page.url()).pathname
  })

  await test.step('open the evidence', async () => {
    await expect(page.getByText(/Timeline \(\d+ events\)/)).toBeVisible()
    await expect(page.getByText('trigger', { exact: true }).first()).toBeVisible()
    await expect(page.getByText('pre trigger', { exact: true }).first()).toBeVisible()
    await expect(page.getByText('blocking snapshot', { exact: true }).first()).toBeVisible()
    // Stored graphs render on demand from the recorded snapshot.
    await page.getByRole('button', { name: 'Show blocking graph' }).first().click()
    await expect(page.getByRole('list', { name: /Blocking tree/ }).first()).toBeVisible()
    await expectAccessible(page, 'incident detail')
  })

  const reportPath = testInfo.outputPath('incident-report.html')
  await test.step('export the HTML report', async () => {
    const download = page.waitForEvent('download')
    await page.getByRole('link', { name: 'Export HTML report' }).click()
    await (await download).saveAs(reportPath)
    const html = readFileSync(reportPath, 'utf8')
    expect(html).toContain('Blocking graphs')
    expect(html).not.toMatch(/<script/i)
    for (const s of forbidden) expect(html, 'report must not contain credentials').not.toContain(s)
  })

  await test.step('export JSON and import it again', async () => {
    const download = page.waitForEvent('download')
    await page.getByRole('link', { name: 'Export JSON' }).click()
    const jsonPath = testInfo.outputPath('incident.json')
    await (await download).saveAs(jsonPath)
    const json = readFileSync(jsonPath, 'utf8')
    expect(JSON.parse(json).format).toBe('locklens-incident')
    for (const s of forbidden) expect(json, 'export must not contain credentials').not.toContain(s)
    await page.goto('/incidents')
    await page.getByLabel('Import an exported incident (JSON)').setInputFiles(jsonPath)
    await expect(page).toHaveURL(/\/incidents\/\d+/)
    expect(new URL(page.url()).pathname).not.toBe(incidentURL)
    await expect(page.getByText('import', { exact: true })).toBeVisible()
  })

  await test.step('open the report offline', async () => {
    const ctx = await browser.newContext({ offline: true, javaScriptEnabled: false })
    const offline = await ctx.newPage()
    const requests: string[] = []
    offline.on('request', (r) => requests.push(r.url()))
    const url = pathToFileURL(reportPath).href
    await offline.goto(url)
    await expect(offline.getByRole('heading', { name: 'Blocking graphs' })).toBeVisible()
    await expect(offline.getByRole('heading', { name: 'Timeline' })).toBeVisible()
    expect(requests.filter((r) => r !== url), 'the report must not load anything').toEqual([])
    await ctx.close()
  })

  await test.step('remaining pages render real data', async () => {
    for (const [path, heading] of [
      [`${targetURL}/queries`, /Query workload/],
      [`${targetURL}/sessions`, /Sessions and long transactions/],
      [`${targetURL}/tables`, /Tables and vacuum/],
      [`${targetURL}/replication`, /Replication/],
      ['/settings', 'Targets and settings'],
      ['/lab', 'Lab guide'],
    ] as const) {
      await page.goto(path)
      await expect(page.getByRole('heading', { name: heading }).first()).toBeVisible()
      await expectAccessible(page, path)
    }
    await page.goto('/settings')
    await expect(page.getByRole('table', { name: 'Monitoring store tables' })).toContainText('metric_points')
    await page.goto(incidentURL)
    await page.getByRole('button', { name: /Switch to (dark|light) theme/ }).click()
    await expectAccessible(page, 'incident detail, other theme')
  })

  await test.step('sign out', async () => {
    await page.getByRole('button', { name: 'Log out' }).click()
    await expect(page.getByRole('heading', { name: 'Sign in to LockLens' })).toBeVisible()
    const res = await page.request.get('/api/v1/targets')
    expect(res.status()).toBe(401)
  })

  expect(consoleErrors, 'browser console errors').toEqual([])
})
