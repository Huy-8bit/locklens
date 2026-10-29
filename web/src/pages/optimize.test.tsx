import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { OptimizerResponse, QueryInsightResponse, Recommendation } from '../api/types'
import { StatementOptimization } from '../components/RecommendationParts'
import { changeLabel } from '../lib/optimizer'
import { axeViolations, mockFetch, renderAt } from '../test/utils'
import OptimizePage from './OptimizePage'

afterEach(() => vi.unstubAllGlobals())

const rec = (over: Partial<Recommendation>): Recommendation => ({
  id: 1,
  target_id: 1,
  kind: 'missing_index',
  key: 'shop.orders(customer_id)',
  status: 'open',
  severity: 'warning',
  title: 'Index on shop.orders (customer_id)',
  summary: '2 statement(s) filter shop.orders (about 2.0M rows) on customer_id without an index starting with customer_id.',
  details: {
    table: 'shop.orders',
    columns: ['customer_id'],
    rows: 2e6,
    seq_scan: 900,
    idx_scan: 10,
    statements: [{ identity_id: 9, queryid: '42', share_of_load: 0.31, calls: 1200, mean_ms: 48, text: 'SELECT id FROM shop.orders WHERE customer_id = $1' }],
  },
  ddl: 'CREATE INDEX CONCURRENTLY ON shop.orders (customer_id);',
  benefit: 31,
  confidence: { level: 'medium', reasons: ['shop.orders was read sequentially 900 times'] },
  first_seen: '2026-10-29T10:00:00Z',
  last_seen: '2026-10-29T12:00:00Z',
  decided_by: null,
  decided_at: null,
  note: '',
  applied_at: null,
  applied_how: null,
  before_ms: null,
  after_ms: null,
  verified_at: null,
  whatif: null,
  resolved_at: null,
  target_name: 'orders-db',
  ...over,
})

const overview: OptimizerResponse = {
  recommendations: [
    rec({}),
    rec({ id: 2, kind: 'unused_index', key: 'shop.orders_status_idx', title: 'Unused index shop.orders_status_idx (50 MiB)', severity: 'info', benefit: 7, ddl: 'DROP INDEX CONCURRENTLY shop.orders_status_idx;', details: { index: 'shop.orders_status_idx', size_bytes: 50 << 20 } }),
    rec({ id: 3, kind: 'fk_without_index', status: 'verified', title: 'Index the foreign key items_order_fk', benefit: 20, before_ms: 100, after_ms: 18, applied_at: '2026-10-28T10:00:00Z', applied_how: 'index shop.items_order_idx was created' }),
  ],
  targets: [{ target_id: 1, target_name: 'orders-db', catalog_at: '2026-10-29T11:55:00Z', database: 'app', query_text_enabled: true, hypopg_enabled: false, hypopg_installed: '1.4.1', sql_enabled: true, warnings: [] }],
  counts: { open: 2, verified: 1 },
  parser_available: true,
  note: 'LockLens never changes the database.',
}

describe('OptimizePage', () => {
  it('lists recommendations by benefit and records a decision', async () => {
    const calls = mockFetch({
      '/api/v1/optimizer': () => ({ body: overview }),
      '/api/v1/targets/1/recommendations/1/decision': () => ({ body: rec({ status: 'accepted', decided_by: 'admin', decided_at: '2026-10-29T12:01:00Z', note: 'tonight' }) }),
    })
    const { container } = renderAt('/optimize', '/optimize', <OptimizePage />)
    const table = await screen.findByRole('table', { name: /Recommendations/ })
    const rows = within(table).getAllByRole('row').slice(1)
    expect(rows.map((r) => within(r).getAllByRole('cell')[0]?.textContent)).toEqual(['31', '7', '20'])
    expect(within(rows[2] as HTMLElement).getByText('−82%')).toBeInTheDocument()
    expect(screen.getByText('HypoPG installed, off')).toBeInTheDocument()

    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /Index on shop.orders \(customer_id\)/ }))
    const dialog = await screen.findByRole('dialog')
    expect(dialog.querySelector('pre')?.textContent).toBe('CREATE INDEX CONCURRENTLY ON shop.orders (customer_id);')
    expect(within(dialog).getByRole('link', { name: 'queryid 42' })).toHaveAttribute('href', '/queries/1/9')
    // HypoPG is installed but not enabled for this instance: no what-if button.
    expect(within(dialog).queryByRole('button', { name: /hypothetical index/ })).not.toBeInTheDocument()
    expect(within(dialog).getByText(/What-if checks are turned off/)).toBeInTheDocument()
    await user.type(within(dialog).getByLabelText(/Note/), 'tonight')
    await user.click(within(dialog).getByRole('button', { name: 'Accept' }))
    await waitFor(() => expect(calls.find((c) => c.method === 'POST')?.body).toBe(JSON.stringify({ status: 'accepted', note: 'tonight' })))
    expect(await axeViolations(container)).toEqual([])
  })

  it('says what the optimizer cannot see', async () => {
    mockFetch({
      '/api/v1/optimizer': () => ({
        body: { ...overview, recommendations: [], parser_available: false, targets: [{ ...overview.targets[0], catalog_at: null, query_text_enabled: false }] },
      }),
    })
    renderAt('/optimize', '/optimize', <OptimizePage />)
    expect(await screen.findByText(/This build of LockLens has no SQL parser/)).toBeInTheDocument()
    expect(screen.getByText(/query text is not collected/)).toBeInTheDocument()
    expect(screen.getByText('No recommendations')).toBeInTheDocument()
  })
})

describe('StatementOptimization', () => {
  it('shows parser lints and the statement’s recommendations', async () => {
    const insight = {
      analysis: {
        available: true,
        result: {
          fingerprint: 'a',
          command: 'select',
          tables: [{ schema: 'shop', name: 'orders' }],
          predicates: [{ table: 'shop.orders', column: 'email', op: '=', wrapped: 'lower' }],
          order_by: [],
          has_limit: false,
          lints: [{ code: 'function_on_column', severity: 'warning', message: 'lower(email) in a condition cannot use a plain index on email.' }],
        },
      },
      recommendations: [rec({})],
    } as unknown as QueryInsightResponse
    renderAt('/', '/', <StatementOptimization insight={insight} />)
    expect(screen.getByText(/cannot use a plain index/)).toBeInTheDocument()
    expect(screen.getByText('shop.orders.lower(email) =')).toBeInTheDocument()
    await userEvent.setup().click(screen.getByRole('button', { name: /Index on shop.orders/ }))
    expect(screen.getByText('SQL to apply')).toBeInTheDocument()
  })

  it('formats changes', () => {
    expect(changeLabel(100, 18)).toBe('−82%')
    expect(changeLabel(100, 130)).toBe('+30%')
    expect(changeLabel(null, 1)).toBeNull()
  })
})
