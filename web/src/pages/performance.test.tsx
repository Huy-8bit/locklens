import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Investigation, PerfItem, PerformanceResponse } from '../api/types'
import { InvestigationView } from '../components/InsightParts'
import { bandPoints, impactTone, warmupLabel } from '../lib/insight'
import { axeViolations, mockFetch, renderAt } from '../test/utils'
import PerformancePage from './PerformancePage'

afterEach(() => vi.unstubAllGlobals())

const item = (over: Partial<PerfItem> & { score: number; rule: string; title: string }): PerfItem => ({
  target_id: 1,
  target_name: 'orders-db',
  finding_id: over.score,
  first_seen: '2026-10-24T12:00:00Z',
  last_seen: '2026-10-24T12:10:00Z',
  statement: null,
  finding: {
    rule_id: over.rule,
    rule_version: 2,
    key: 'q:18:5:10:true:42',
    severity: 'warning',
    title: over.title,
    status: 'active',
    observed_from: '2026-10-24T11:45:00Z',
    observed_to: '2026-10-24T12:10:00Z',
    observed: ['Time per call is 10 ms, against a usual 2 ms.'],
    evidence: [{ label: 'queryid', value: '42', source: 'pg_stat_statements' }],
    explanation: 'Each execution takes longer than usual.',
    possible_explanations: [],
    next_checks: [],
    limitations: '',
    confidence: { level: 'high', reasons: ['compared with 7 points of its own history'] },
    metrics: { running_seconds: 600 },
  },
  impact: {
    score: over.score,
    confidence_factor: 1,
    components: [
      { name: 'Share of execution time', points: 15, max: 30, detail: '50.0% of all statement execution time in the window' },
      { name: 'Severity', points: 12, max: 20, detail: 'rule severity warning' },
    ],
  },
  ...over,
})

const perf: PerformanceResponse = {
  generated_at: '2026-10-24T12:10:00Z',
  anomalies: [
    item({ score: 72, rule: 'queries.mean_regression', title: 'Query 42 is 5.0x slower per call than usual', statement: { key: { server_major: 18, dbid: 5, userid: 10, toplevel: true, queryid: '42' }, identity_id: 9, datname: 'app', username: 'web', command: 'select', query_text: 'SELECT * FROM orders WHERE id = $1' } }),
    item({ score: 20, rule: 'queries.possible_n_plus_one', title: 'Possible N+1 pattern: query 7 runs 80 times per second' }),
  ],
  running: [item({ score: 50, rule: 'queries.long_running', title: 'Query 3 running for 10m0s in pid 77' })],
  unavailable: [{ target_id: 2, target_name: 'legacy-db', reason: 'extension not installed' }],
  insufficient: [],
  baselines: [{ target_id: 1, target_name: 'orders-db', statements: 12, warm: 9 }],
  baseline_config: { days: 7, point_seconds: 3600, warmup_points: 24, sensitivity: 4 },
  percentiles: { available: false, reason: 'pg_stat_statements keeps totals per statement, not individual executions.' },
}

describe('PerformancePage', () => {
  it('ranks anomalies by impact, explains the score, and lists running queries separately', async () => {
    mockFetch({ '/api/v1/performance': () => ({ body: perf }) })
    const { container } = renderAt('/performance', '/performance', <PerformancePage />)
    const table = await screen.findByRole('table', { name: /Statement anomalies/ })
    const rows = within(table).getAllByRole('row').slice(1)
    expect(rows).toHaveLength(2)
    const [top] = rows
    if (!top) throw new Error('no anomaly rows')
    expect(within(top).getByText('72')).toBeInTheDocument()
    expect(within(top).getByRole('link', { name: /SELECT \* FROM orders/ })).toHaveAttribute('href', '/queries/1/9')
    expect(screen.getByText(/legacy-db: pg_stat_statements is not available/)).toBeInTheDocument()
    expect(screen.getByText('9 / 12')).toBeInTheDocument()

    const user = userEvent.setup()
    await user.click(within(top).getByRole('button', { name: /slower per call/ }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('50.0% of all statement execution time in the window')).toBeInTheDocument()
    expect(within(dialog).getByText(/it is not a probability/)).toBeInTheDocument()
    await user.keyboard('{Escape}')

    await user.click(screen.getByRole('tab', { name: /Running now/ }))
    const running = await screen.findByRole('table', { name: /Queries running for long/ })
    expect(within(running).getByText('10m 0s')).toBeInTheDocument()
    expect(await axeViolations(container)).toEqual([])
  })

  it('says when nothing is anomalous instead of showing an empty table', async () => {
    mockFetch({ '/api/v1/performance': () => ({ body: { ...perf, anomalies: [], running: [], unavailable: [] } }) })
    renderAt('/performance', '/performance', <PerformancePage />)
    expect(await screen.findByText('No anomalies')).toBeInTheDocument()
  })
})

describe('InvestigationView', () => {
  it('shows ranked hypotheses with evidence for and against, and what is missing', async () => {
    const inv: Investigation = {
      at: '2026-10-24T12:10:00Z',
      since: '2026-10-24T12:00:00Z',
      summary: 'The evidence most supports: waiting for locks held by other sessions (strong evidence). This is a hypothesis to check, not a confirmed cause.',
      hypotheses: [
        { id: 'lock_contention', title: 'Waiting for locks held by other sessions', score: 0.85, strength: 'strong', supporting: ['75% of the statement’s sampled time waits on locks.'], contradicting: [], next_steps: ['Open the blocking explorer.'] },
        { id: 'plan_change', title: 'A different execution plan or access path', score: 0.2, strength: 'weak', supporting: ['Blocks read per call are 2.0x the usual level.'], contradicting: ['Rows per call also grew.'], next_steps: ['Compare plans.'] },
      ],
      timeline: [{ at: '2026-10-24T11:58:00Z', kind: 'finding', rule_id: 'blocking.long_chain', summary: 'Blocking chain rooted at pid 9' }],
      missing_evidence: ['No execution plan of this statement was saved.'],
      disclaimer: 'none is a confirmed cause',
    }
    render(<InvestigationView investigation={inv} />)
    expect(screen.getByText(/not a confirmed cause/)).toBeInTheDocument()
    expect(screen.getByText('strong evidence')).toBeInTheDocument()
    expect(screen.getByText('75% of the statement’s sampled time waits on locks.')).toBeInTheDocument()
    // The second hypothesis is collapsed until opened.
    expect(screen.queryByText('Rows per call also grew.')).not.toBeInTheDocument()
    await userEvent.setup().click(screen.getByRole('button', { name: /A different execution plan/ }))
    expect(screen.getByText('Rows per call also grew.')).toBeInTheDocument()
    expect(screen.getByText('No execution plan of this statement was saved.')).toBeInTheDocument()
    expect(screen.getByText('Blocking chain rooted at pid 9')).toBeInTheDocument()
  })
})

describe('insight helpers', () => {
  it('formats bands, impact tones, and warm-up', () => {
    expect(bandPoints([{ t: '2026-10-24T12:00:00Z', median: 2, low: 1, high: 3, basis: 'all hours' }])).toEqual([[Date.parse('2026-10-24T12:00:00Z'), 1, 3]])
    expect(bandPoints(undefined)).toEqual([])
    expect([impactTone(80), impactTone(40), impactTone(5)]).toEqual(['crit', 'warn', 'good'])
    expect(warmupLabel(6, 24)).toBe('learning: 6 of 24 points')
    expect(warmupLabel(30, 24)).toBe('30 points')
  })
})
