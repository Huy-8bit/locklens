import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { FindingCard } from './FindingCard'
import type { Finding } from '../api/types'
import { axeViolations } from '../test/utils'

const base: Finding = {
  rule_id: 'blocking.long_chain', rule_version: 1, key: 'pid:1', severity: 'warning', title: 'Blocking chain rooted at pid 1 persists', status: 'active',
  observed_from: '2026-10-24T10:00:00Z', observed_to: '2026-10-24T10:01:00Z', observed: ['observed fact'], evidence: [{ label: 'longest lock wait', value: 90, unit: 's', source: 'pg_locks.waitstart' }, { label: 'unknown value', value: null, source: 'x' }],
  explanation: 'explanation text', possible_explanations: ['maybe this'], next_checks: ['check that'], limitations: 'not a deadlock',
}

describe('FindingCard', () => {
  it('shows evidence with units, possible explanations, and limitations', async () => {
    const { container } = render(<ul><FindingCard finding={base} /></ul>)
    expect(screen.getByText('1m 30s')).toBeInTheDocument()
    expect(screen.getByText('unknown')).toBeInTheDocument()
    expect(screen.getByText(/Possible explanations \(not confirmed\)/)).toBeInTheDocument()
    expect(screen.getByText(/not a deadlock/)).toBeInTheDocument()
    expect(await axeViolations(container)).toEqual([])
  })
  it('renders insufficient data as not evaluated, never as a pass', () => {
    render(<ul><FindingCard finding={{ ...base, status: 'insufficient_data', explanation: 'pg_stat_statements is not available' }} /></ul>)
    expect(screen.getByText('not evaluated')).toBeInTheDocument()
    expect(screen.getByText(/not available/)).toBeInTheDocument()
  })
})
