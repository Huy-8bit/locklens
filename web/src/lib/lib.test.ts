import { describe, expect, it } from 'vitest'
import { commandColor, commandGroup } from './commands'
import { bytes, compact, ms, num, pct, rate, seconds, UNKNOWN } from './format'

describe('format', () => {
  it('renders unknown values as unknown, never as zero', () => {
    for (const f of [num, compact, rate, pct, bytes, ms, seconds]) {
      expect(f(null)).toBe(UNKNOWN)
      expect(f(undefined)).toBe(UNKNOWN)
      expect(f(Number.NaN)).toBe(UNKNOWN)
    }
  })
  it('keeps real zero', () => {
    expect(num(0)).toBe('0')
    expect(bytes(0)).toBe('0 B')
    expect(pct(0)).toBe('0.0%')
  })
  it('formats magnitudes', () => {
    expect(compact(1530)).toBe('1.5k')
    expect(compact(2_400_000)).toBe('2.4M')
    expect(rate(0.004)).toBe('<0.01/s')
    expect(bytes(1536)).toBe('1.5 KiB')
    expect(ms(0.42)).toBe('0.42 ms')
    expect(ms(1500)).toBe('1.5 s')
    expect(seconds(125)).toBe('2m 5s')
  })
})

describe('command colors', () => {
  it('assigns a fixed color per class and folds rare classes into other', () => {
    expect(commandColor('select')).toBe('var(--s1)')
    expect(commandColor('delete')).toBe('var(--s4)')
    expect(commandColor('transaction')).toBe('var(--s-other)')
    expect(commandGroup('utility')).toBe('other')
    expect(commandGroup('insert')).toBe('insert')
  })
})
