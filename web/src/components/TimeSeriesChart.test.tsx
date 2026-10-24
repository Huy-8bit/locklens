import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { TimeSeriesChart } from './TimeSeriesChart'

describe('TimeSeriesChart', () => {
  it('shows an explicit empty state instead of a zero line when every bucket is unknown', () => {
    render(
      <TimeSeriesChart
        ariaLabel="Commits"
        series={[{ key: 's0', label: 'commits', series: { metric: 'xact.commit.per_s', aggregation: 'avg', points: [{ t: '2026-10-24T10:00:00Z', v: null, n: 1, known: 0 }] } }]}
        stepSeconds={10}
        format={(v) => String(v)}
        from="2026-10-24T09:00:00Z"
        to="2026-10-24T10:00:00Z"
      />,
    )
    expect(screen.getByText('No samples in this time range')).toBeInTheDocument()
    expect(screen.getByRole('figure', { name: 'Commits' })).toBeInTheDocument()
  })
})
