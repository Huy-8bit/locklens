import { defineConfig, devices } from '@playwright/test'

// Browser end-to-end tests run against a real LockLens stack started by
// scripts/e2e.sh (no API mocks). Run them through that script.
const baseURL = process.env.LOCKLENS_E2E_BASE_URL
if (!baseURL) {
  throw new Error('LOCKLENS_E2E_BASE_URL is not set; run ./scripts/e2e.sh from the repository root')
}

export default defineConfig({
  testDir: './browser',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 6 * 60_000,
  expect: { timeout: 15_000 },
  outputDir: '../../test-results/e2e-artifacts',
  reporter: [['list'], ['html', { outputFolder: '../../test-results/e2e-report', open: 'never' }]],
  use: {
    baseURL,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    // axe-core is injected as an inline script, which the server's CSP blocks.
    bypassCSP: true,
    acceptDownloads: true,
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
