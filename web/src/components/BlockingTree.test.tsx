import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { BlockingTree } from './BlockingTree'
import type { Report, ReportNode } from '../api/types'
import { axeViolations } from '../test/utils'

function node(id: number, blocks: number[], blockedBy: number[], extra: Partial<ReportNode> = {}): ReportNode {
  return { id, kind: 'session', blocks, blocked_by: blockedBy, direct_blocked_count: blocks.length, transitive_blocked_count: blocks.length, root: blockedBy.length === 0, in_cycle: false, component: 1, ...extra }
}

function report(nodes: ReportNode[], comps: Report['analysis']['components']): Report {
  return {
    snapshot: {
      schema_version: 1, collected_at: '', server_time: '', collection: { duration_ms: 1, include_query: false, query_max_length: 200, min_tx_age_seconds: 60, atomic: false },
      server: { host: 'h', port: 5432, database: 'd', user: 'u', version: '18', version_num: 180000, read_all_stats: true, track_activities: true },
      sessions: [], edges: [], prepared_transactions: [], warnings: [],
    },
    analysis: { min_tx_age_seconds: 60, summary: { blocked_sessions: 0, blocking_sessions: 0, root_blockers: 0, cyclic_components: 0, long_transactions: 0, idle_in_transaction: 0 }, nodes, edges: [], components: comps, sessions: [], long_transactions: [], warnings: [] },
  }
}

describe('BlockingTree', () => {
  it('shows a no-blocking message for an empty graph', () => {
    render(<BlockingTree report={report([], [])} selected={null} onSelect={() => {}} />)
    expect(screen.getByText(/No blocking detected/)).toBeInTheDocument()
  })

  it('renders shared descendants once and references them afterwards', () => {
    // 1 -> 2 -> 4, 1 -> 3 -> 4
    const r = report(
      [node(1, [2, 3], [], { transitive_blocked_count: 3 }), node(2, [4], [1]), node(3, [4], [1]), node(4, [], [2, 3])],
      [{ id: 1, members: [1, 2, 3, 4], roots: [1], cyclic: false, cycle_members: [], entries: [1] }],
    )
    render(<BlockingTree report={r} selected={null} onSelect={() => {}} />)
    expect(screen.getAllByText('pid 4')).toHaveLength(2)
    expect(screen.getByText('(shown above)')).toBeInTheDocument()
    expect(screen.getByText('root blocker')).toBeInTheDocument()
    expect(screen.getByText(/blocks 2 directly · 3 total/)).toBeInTheDocument()
  })

  it('terminates on cycles and never calls them deadlocks', () => {
    const r = report(
      [node(1, [2], [2], { in_cycle: true, root: false }), node(2, [1], [1], { in_cycle: true, root: false })],
      [{ id: 1, members: [1, 2], roots: [], cyclic: true, cycle_members: [1, 2], entries: [] }],
    )
    render(<BlockingTree report={r} selected={null} onSelect={() => {}} />)
    expect(screen.getByText(/not a confirmed deadlock/)).toBeInTheDocument()
    expect(screen.getByText('(cycle back to an ancestor)')).toBeInTheDocument()
    expect(screen.queryByText('root blocker')).not.toBeInTheDocument()
  })

  it('labels PID 0 as a prepared transaction and supports keyboard selection', async () => {
    const onSelect = vi.fn()
    const r = report([node(0, [7], [], { kind: 'prepared_transaction' }), node(7, [], [0])], [{ id: 1, members: [0, 7], roots: [0], cyclic: false, cycle_members: [], entries: [0] }])
    const { container } = render(<BlockingTree report={r} selected={null} onSelect={onSelect} />)
    const btn = screen.getByRole('button', { name: /prepared transaction \(PID 0\)/ })
    btn.focus()
    await userEvent.keyboard('{Enter}')
    expect(onSelect).toHaveBeenCalledWith(0)
    expect(await axeViolations(container)).toEqual([])
  })
})
