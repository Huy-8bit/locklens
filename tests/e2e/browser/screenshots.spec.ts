import { spawn, spawnSync, type ChildProcess } from 'node:child_process'
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type APIRequestContext, type Page } from '@playwright/test'

// Documentation screenshots of alerting and integrations, taken from a real
// lab run: a blocking chain and a stopped standby open incidents, which
// reach Alertmanager through LockLens' channel, while Prometheus scrapes the
// exports. Run it with the observability overlay:
//
//   LOCKLENS_E2E_OBSERVABILITY=1 LOCKLENS_E2E_SCREENSHOTS=$PWD/docs/images ./scripts/e2e.sh screenshots.spec.ts

const dir = process.env.LOCKLENS_E2E_SCREENSHOTS ?? ''
const project = process.env.LOCKLENS_E2E_COMPOSE_PROJECT ?? ''
const composeFiles = (process.env.LOCKLENS_E2E_COMPOSE_FILES ?? '').split(',')
const composeEnvFile = process.env.LOCKLENS_E2E_COMPOSE_ENV_FILE ?? ''
const adminPassword = process.env.LOCKLENS_E2E_ADMIN_PASSWORD ?? ''
const prometheus = process.env.LOCKLENS_E2E_PROMETHEUS_URL ?? ''
const alertmanager = process.env.LOCKLENS_E2E_ALERTMANAGER_URL ?? ''

test.skip(!dir || !prometheus, 'set LOCKLENS_E2E_SCREENSHOTS and LOCKLENS_E2E_OBSERVABILITY=1')
test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'light' })
test.setTimeout(20 * 60_000)

function compose(...args: string[]): string[] {
  return ['compose', '-p', project, ...composeFiles.flatMap((f) => ['-f', f]), '--env-file', composeEnvFile, ...args]
}

const scenarios: ChildProcess[] = []
test.afterAll(() => {
  for (const s of scenarios) s.kill('SIGINT')
  spawnSync('docker', compose('start', 'standby-3'), { stdio: 'ignore', timeout: 120_000 })
  spawnSync('docker', compose('run', '--rm', 'lab', 'stop'), { stdio: 'ignore', timeout: 60_000 })
})

/** Waits for charts and lists to settle, then saves a viewport screenshot. */
async function shot(page: Page, name: string) {
  mkdirSync(dir, { recursive: true })
  // Pages that poll (Prometheus, Alertmanager) never go idle: wait a bounded time.
  await page.waitForLoadState('networkidle', { timeout: 5_000 }).catch(() => undefined)
  await page.waitForTimeout(1200)
  await page.screenshot({ path: join(dir, `${name}.png`) })
}

async function api(request: APIRequestContext, csrf: string, method: 'POST' | 'PUT', path: string, data: unknown) {
  const r = await request.fetch(path, { method, data, headers: { 'X-CSRF-Token': csrf } })
  expect(r.ok(), `${method} ${path}: ${r.status()} ${await r.text()}`).toBeTruthy()
  return r.json()
}

type Target = { id: number; name: string; environment: string }

test('alerting and integrations, from a lab run', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('Username').fill('admin')
  await page.getByLabel('Password').fill(adminPassword)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page).toHaveURL(/\/overview$/)
  const csrf = await page.evaluate(() => fetch('/api/v1/session').then((r) => r.json()).then((s: { csrf_token: string }) => s.csrf_token))
  const { targets } = (await (await page.request.get('/api/v1/targets')).json()) as { targets: Target[] }
  const byName = (n: string) => targets.find((t) => t.name === n) as Target
  const primary = byName('lab-primary')
  const standby3 = byName('lab-standby-3')

  await test.step('configure alerting the way a team would', async () => {
    await api(page.request, csrf, 'PUT', '/api/v1/settings/alerting', {
      modes: { 'deadlocks.increase': 'incident', 'queries.possible_n_plus_one': 'off' },
      overrides: {
        environments: { lab: { modes: { 'temp_files.increase': 'incident' }, thresholds: { blocking_min_seconds: 15, connections_warn_ratio: 0.7 } } },
        targets: { [standby3.id]: { modes: { 'replication.replica_lag': 'finding' }, thresholds: { target_down_seconds: 20 } } },
      },
    })
    await api(page.request, csrf, 'POST', '/api/v1/notifications/channels', {
      name: 'Alertmanager — on-call', kind: 'alertmanager', enabled: true, min_severity: 'warning', environments: [], rule_ids: [], target_ids: [],
      delay_minutes: 0, on_open: true, on_escalate: true, on_resolve: true, reminder_minutes: 0, destination: { url: 'http://alertmanager:9093' },
    })
    await api(page.request, csrf, 'POST', '/api/v1/notifications/channels', {
      name: 'Escalation — DBA lead', kind: 'alertmanager', enabled: true, min_severity: 'critical', environments: ['lab'],
      rule_ids: ['target.unreachable', 'blocking.long_chain'], target_ids: [], delay_minutes: 15, on_open: true, on_escalate: true, on_resolve: true,
      reminder_minutes: 0, destination: { url: 'http://alertmanager:9093' },
    })
    await api(page.request, csrf, 'POST', '/api/v1/notifications/maintenance', {
      name: 'Sunday patching', enabled: true, kind: 'weekly', environments: ['lab'], target_ids: [], rule_ids: [], starts_at: null, ends_at: null,
      weekdays: [0], start_time: '02:00', duration_minutes: 120, timezone: 'Europe/Berlin',
    })
    await api(page.request, csrf, 'POST', '/api/v1/notifications/maintenance', {
      name: 'Quiet after deploys', enabled: true, kind: 'after_deploy', environments: [], target_ids: [primary.id], rule_ids: ['queries.mean_regression'],
      starts_at: null, ends_at: null, weekdays: [], start_time: '', duration_minutes: 15, timezone: 'UTC',
    })
  })

  await test.step('cause incidents: a blocking chain and a stopped standby', async () => {
    scenarios.push(spawn('docker', compose('run', '--rm', 'lab', 'run', 'chain', '--duration', '6m'), { stdio: 'ignore' }))
    expect(spawnSync('docker', compose('stop', 'standby-3'), { timeout: 120_000 }).status).toBe(0)
    await expect
      .poll(
        async () => {
          const r = (await (await page.request.get('/api/v1/incidents?state=open&page_size=50')).json()) as { incidents: { rule_id: string }[] }
          const rules = new Set(r.incidents.map((i) => i.rule_id))
          return rules.has('target.unreachable') && rules.has('blocking.long_chain')
        },
        { timeout: 5 * 60_000, intervals: [5_000] },
      )
      .toBe(true)
  })

  await test.step('LockLens: rules, overrides, notifications, tokens', async () => {
    await page.goto('/settings/rules')
    await expect(page.getByRole('heading', { name: 'What each rule does' })).toBeVisible()
    await shot(page, 'alert-rules')

    await page.getByRole('heading', { name: 'Overrides' }).scrollIntoViewIfNeeded()
    await page.getByLabel('Instance', { exact: true }).selectOption(String(standby3.id))
    await expect(page.getByText('Thresholds that differ from the global ones')).toBeVisible()
    await page.getByRole('heading', { name: 'Overrides' }).evaluate((h) => h.scrollIntoView({ block: 'start' }))
    await shot(page, 'alert-overrides')

    await page.getByRole('button', { name: 'Edit' }).first().click()
    await expect(page.getByRole('dialog')).toBeVisible()
    await shot(page, 'alert-override-edit')
    await page.keyboard.press('Escape')

    await page.goto('/settings/notifications')
    await expect(page.getByText('Alertmanager — on-call').first()).toBeVisible()
    await shot(page, 'notifications')
    await page.getByRole('heading', { name: 'Maintenance windows' }).evaluate((h) => h.scrollIntoView({ block: 'center' }))
    await expect(page.getByText('Sunday patching').first()).toBeVisible()
    await shot(page, 'maintenance-windows')

    await page.getByRole('button', { name: 'Add channel' }).click()
    await page.getByLabel('Type').selectOption('alertmanager')
    await page.getByLabel('Name').fill('Alertmanager — staging')
    await page.getByLabel('Alertmanager URL').fill('http://alertmanager:9093')
    await page.getByLabel('Escalation').selectOption('30')
    await shot(page, 'channel-alertmanager')
    await page.keyboard.press('Escape')

    await page.goto('/settings/tokens')
    await page.getByRole('button', { name: 'New token' }).click()
    await page.getByLabel('Name').fill('prometheus')
    await page.getByLabel('Allows').selectOption('metrics')
    await page.getByRole('button', { name: 'Create token' }).click()
    await expect(page.getByText('Copy this token now')).toBeVisible()
    await shot(page, 'api-tokens-prometheus')
    // The frontend proxies /metrics/targets: the snippet's address works.
    const secret = (await page.locator('code.select-all').innerText()).trim()
    const scrape = await page.request.get('/metrics/targets', { headers: { Authorization: `Bearer ${secret}` } })
    expect(scrape.status()).toBe(200)
    expect(await scrape.text()).toContain('locklens_target_up{target="lab-primary",environment="lab"} 1')
  })

  await test.step('LockLens: the incidents', async () => {
    await page.goto('/incidents')
    await expect(page.getByText(/cannot be reached/).first()).toBeVisible()
    await shot(page, 'incidents-alerting')
    await page.getByText(/cannot be reached/).first().click()
    await expect(page.getByRole('heading', { name: /cannot be reached/ })).toBeVisible()
    await shot(page, 'incident-unreachable')
  })

  await test.step('Prometheus scrapes the exports and evaluates the rules', async () => {
    await expect
      .poll(async () => (await (await page.request.get(`${prometheus}/api/v1/query?query=locklens_target_up`)).json()).data?.result?.length ?? 0, {
        timeout: 2 * 60_000,
        intervals: [5_000],
      })
      .toBeGreaterThan(3)
    await page.goto(`${prometheus}/targets`)
    await expect(page.getByText('locklens-targets').first()).toBeVisible()
    await shot(page, 'prometheus-targets')
    // PostgresInstanceDown fires after two minutes of locklens_target_up == 0.
    await expect
      .poll(
        async () => {
          const alerts = ((await (await page.request.get(`${prometheus}/api/v1/alerts`)).json()).data?.alerts ?? []) as { labels: Record<string, string>; state: string }[]
          return alerts.some((a) => a.labels.alertname === 'PostgresInstanceDown' && a.state === 'firing')
        },
        { timeout: 4 * 60_000, intervals: [10_000] },
      )
      .toBe(true)
    await page.goto(`${prometheus}/alerts`)
    await page.waitForTimeout(2000)
    await shot(page, 'prometheus-alerts')
  })

  await test.step('Alertmanager receives LockLens incidents and Prometheus alerts', async () => {
    await expect
      .poll(async () => JSON.stringify(await (await page.request.get(`${alertmanager}/api/v2/alerts`)).json()), { timeout: 3 * 60_000, intervals: [5_000] })
      .toMatch(/target\.unreachable[\s\S]*|[\s\S]*target\.unreachable/)
    await page.goto(`${alertmanager}/#/alerts`)
    await page.waitForTimeout(3000)
    await shot(page, 'alertmanager')
  })

  await test.step('the instance is reachable again', async () => {
    expect(spawnSync('docker', compose('start', 'standby-3'), { timeout: 120_000 }).status).toBe(0)
    await expect
      .poll(
        async () => {
          const r = (await (await page.request.get(`/api/v1/incidents?target=${standby3.id}&page_size=5`)).json()) as { incidents: { rule_id: string; state: string }[] }
          return r.incidents.find((i) => i.rule_id === 'target.unreachable')?.state
        },
        { timeout: 4 * 60_000, intervals: [5_000] },
      )
      .toBe('resolved')
    await page.goto(`/databases/${standby3.id}/alerts`)
    await page.getByRole('radio', { name: 'All' }).click()
    await expect(page.getByText(/Instance cannot be reached/).first()).toBeVisible()
    await shot(page, 'instance-alerts-recovered')
    // In Prometheus, the instance's up series dropped to 0 while it was down.
    await page.waitForTimeout(45_000)
    const expr = encodeURIComponent('locklens_target_up')
    await page.goto(`${prometheus}/query?g0.expr=${expr}&g0.tab=graph&g0.range_input=10m`)
    await page.waitForTimeout(3000)
    await shot(page, 'prometheus-graph')
  })
})
