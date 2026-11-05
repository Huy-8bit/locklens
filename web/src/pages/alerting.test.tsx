import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { RuleSettings } from '../api/types'
import { AlertingCard } from '../components/AlertingCard'
import { describeWindow, MaintenanceCard } from '../components/MaintenanceCard'
import { axeViolations, mockFetch, renderAt } from '../test/utils'

afterEach(() => vi.unstubAllGlobals())

const settings: RuleSettings = {
  thresholds: { blocking_min_seconds: 30, target_down_seconds: 60 },
  defaults: { blocking_min_seconds: 30, target_down_seconds: 60 },
  rules: ['target.unreachable', 'blocking.long_chain', 'vacuum.dead_tuples'],
  default_modes: { 'target.unreachable': 'incident', 'blocking.long_chain': 'incident', 'vacuum.dead_tuples': 'finding' },
  modes: {},
  overrides: { environments: { staging: { modes: { 'blocking.long_chain': 'finding' }, thresholds: { blocking_min_seconds: 120 } } }, targets: {} },
  threshold_keys: ['blocking_min_seconds', 'target_down_seconds'],
}
const targets = { targets: [{ id: 1, name: 'orders-db', environment: 'staging' }] }

describe('AlertingCard', () => {
  it('changes a rule mode everywhere and shows what applies to an instance', async () => {
    const calls = mockFetch({
      '/api/v1/settings/rules': () => ({ body: settings }),
      '/api/v1/settings/alerting': () => ({ body: {} }),
      '/api/v1/targets/1/policy': () => ({
        body: {
          thresholds: { blocking_min_seconds: 120, target_down_seconds: 60 },
          modes: { 'target.unreachable': 'incident', 'blocking.long_chain': 'finding', 'vacuum.dead_tuples': 'finding' },
          sources: { blocking_min_seconds: 'environment', 'mode:blocking.long_chain': 'environment' },
        },
      }),
      '/api/v1/targets': () => ({ body: targets }),
    })
    const { container } = renderAt('/settings/rules', '/settings/:section', <AlertingCard />)
    const select = await screen.findByLabelText('Mode of Dead rows accumulating', { selector: 'select' })
    expect(screen.getByText(/1 rule mode\(s\), 1 threshold\(s\)/)).toBeInTheDocument()
    await userEvent.setup().selectOptions(select, 'incident')
    await waitFor(() => expect(calls.find((c) => c.method === 'PUT')?.body).toContain('"vacuum.dead_tuples":"incident"'))
    // The staging override applies to orders-db: blocking only reports findings there.
    const applies = (await screen.findByText('What applies to an instance')).closest('section') ?? document.body
    expect(await within(applies as HTMLElement).findByText('Thresholds that differ from the global ones')).toBeInTheDocument()
    expect(within(applies as HTMLElement).getByText(/\(global 30/)).toBeInTheDocument()
    expect(await axeViolations(container)).toEqual([])
  })
})

describe('MaintenanceCard', () => {
  it('lists windows in words', async () => {
    mockFetch({
      '/api/v1/notifications/maintenance': () => ({
        body: {
          windows: [
            { id: 1, name: 'Sunday patching', enabled: true, kind: 'weekly', environments: ['staging'], target_ids: [], rule_ids: [], starts_at: null, ends_at: null,
              weekdays: [0], start_time: '02:00', duration_minutes: 120, timezone: 'UTC', created_by: 'admin', created_at: '', updated_at: '' },
          ],
        },
      }),
      '/api/v1/targets': () => ({ body: targets }),
    })
    renderAt('/settings/notifications', '/settings/:section', <MaintenanceCard />)
    expect(await screen.findByText('Sunday patching')).toBeInTheDocument()
    expect(screen.getByText('Sun at 02:00 (UTC) for 120 min')).toBeInTheDocument()
    expect(describeWindow({ kind: 'after_deploy', starts_at: null, ends_at: null, weekdays: [], start_time: '', duration_minutes: 30, timezone: 'UTC' })).toBe(
      '30 min after each reported deploy or migration',
    )
  })
})
