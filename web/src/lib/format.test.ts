import { describe, expect, it } from 'vitest'
import { fmtBytes, fmtDuration, fmtMs, fmtNumber, fmtPercent, fmtRate, fmtUnit, UNKNOWN } from './format'

describe('formatters never turn unknown into zero', () => {
  it('renders null and undefined as unknown', () => {
    for (const f of [fmtNumber, fmtPercent, fmtBytes, fmtDuration, fmtMs, (v: number | null) => fmtRate(v)]) {
      expect(f(null)).toBe(UNKNOWN)
      expect(f(undefined as unknown as null)).toBe(UNKNOWN)
    }
    expect(fmtUnit(null, 's')).toBe(UNKNOWN)
  })
  it('keeps real zero as zero', () => {
    expect(fmtNumber(0)).toBe('0')
    expect(fmtPercent(0)).toBe('0%')
    expect(fmtBytes(0)).toBe('0 B')
  })
  it('formats values', () => {
    expect(fmtBytes(1536)).toBe('1.5 KiB')
    expect(fmtDuration(125)).toBe('2m 05s')
    expect(fmtDuration(0.25)).toBe('250 ms')
    expect(fmtPercent(0.123)).toBe('12%')
    expect(fmtRate(0.004)).toBe('<0.01/s')
    expect(fmtMs(2500)).toBe('2.5 s')
    expect(fmtUnit(0.5, 'ratio')).toBe('50%')
  })
})
