import type { BandPoint, Confidence, Hypothesis } from '../api/types'

/** Statement rules in words people search for. */
export const STATEMENT_RULES: Record<string, { name: string; short: string }> = {
  'queries.mean_regression': { name: 'Slower per call', short: 'Latency' },
  'queries.call_volume_increase': { name: 'Runs more often', short: 'Volume' },
  'queries.load_increase': { name: 'More execution time', short: 'Load' },
  'queries.io_regression': { name: 'More I/O per call', short: 'I/O' },
  'queries.pattern_change': { name: 'Different rows per call', short: 'Pattern' },
  'queries.possible_n_plus_one': { name: 'Possible N+1', short: 'N+1' },
  'queries.long_running': { name: 'Running for long', short: 'Running' },
}

export function statementRule(id: string) {
  return STATEMENT_RULES[id] ?? { name: id, short: id }
}

export const CONFIDENCE_TONE: Record<Confidence['level'], 'good' | 'warn' | 'muted'> = { high: 'good', medium: 'warn', low: 'muted' }
export const STRENGTH_TONE: Record<Hypothesis['strength'], 'crit' | 'warn' | 'muted'> = { strong: 'crit', moderate: 'warn', weak: 'muted' }

/** Impact score bands for colour. */
export function impactTone(score: number): 'crit' | 'warn' | 'good' {
  if (score >= 60) return 'crit'
  if (score >= 30) return 'warn'
  return 'good'
}

/** Band points for TimeChart: [epoch ms, low, high]. */
export function bandPoints(band: BandPoint[] | undefined): [number, number, number][] {
  return (band ?? []).map((b) => [Date.parse(b.t), b.low, b.high])
}

/** How far a baseline is from being used, as "12 of 24 points". */
export function warmupLabel(points: number, needed: number): string {
  return points >= needed ? `${points} points` : `learning: ${points} of ${needed} points`
}
