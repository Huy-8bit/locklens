import type { RecommendationKind, RecommendationStatus } from '../api/types'

/** What each kind of recommendation is, in words. */
export const KIND_INFO: Record<RecommendationKind, { name: string; what: string }> = {
  missing_index: { name: 'Missing index', what: 'Busy statements filter a large table on columns no index starts with.' },
  fk_without_index: { name: 'Foreign key without index', what: 'Deleting or updating a referenced row scans the referencing table, holding locks meanwhile.' },
  unused_index: { name: 'Unused index', what: 'No query used the index since statistics were reset, yet every write maintains it.' },
  duplicate_index: { name: 'Redundant index', what: 'Another index has the same leading columns and can serve the same lookups.' },
  invalid_index: { name: 'Invalid index', what: 'A failed CREATE INDEX CONCURRENTLY left an index queries cannot use.' },
  stale_statistics: { name: 'Stale statistics', what: 'Many rows changed since the table was last analysed; estimates are outdated.' },
  misestimate: { name: 'Row misestimate', what: 'The planner expects far more or fewer rows than the statement returns.' },
  temp_spill: { name: 'Spills to disk', what: 'Sorts or hashes do not fit in work_mem and write temporary files.' },
  query_shape: { name: 'Statement shape', what: 'A pattern in the SQL that often performs badly, found by PostgreSQL’s parser.' },
}

export function kindInfo(kind: string) {
  return KIND_INFO[kind as RecommendationKind] ?? { name: kind, what: '' }
}

export const STATUS_INFO: Record<RecommendationStatus, { label: string; tone: 'muted' | 'good' | 'warn' | 'crit' | 'accent' }> = {
  open: { label: 'open', tone: 'accent' },
  accepted: { label: 'accepted', tone: 'accent' },
  dismissed: { label: 'dismissed', tone: 'muted' },
  applied: { label: 'applied, measuring', tone: 'warn' },
  verified: { label: 'improved', tone: 'good' },
  no_effect: { label: 'no measurable effect', tone: 'muted' },
  regressed: { label: 'slower after change', tone: 'crit' },
  resolved: { label: 'no longer applies', tone: 'muted' },
}

/** Status filters of the Optimize page. */
export const STATUS_FILTERS = [
  { value: '', label: 'Active' },
  { value: 'open,accepted', label: 'To do' },
  { value: 'applied,verified,no_effect,regressed', label: 'Applied' },
  { value: 'dismissed', label: 'Dismissed' },
  { value: 'all', label: 'All' },
] as const

/** The change in time per call after an applied recommendation, e.g. "−82%". */
export function changeLabel(before: number | null, after: number | null): string | null {
  if (before === null || after === null || before <= 0) return null
  const p = Math.round((after / before - 1) * 100)
  return `${p > 0 ? '+' : p < 0 ? '−' : '±'}${Math.abs(p)}%`
}
