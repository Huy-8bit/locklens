// Statement command classes and their fixed chart colors. Color follows the
// class, never its rank: filters never repaint the remaining classes. The
// seven validated categorical slots go to the first seven classes; the rare
// classes share the neutral "other" color and are grouped as "other" in charts.

export const COMMANDS = ['select', 'insert', 'update', 'delete', 'merge', 'copy', 'ddl', 'transaction', 'maintenance', 'utility', 'other'] as const
export type Command = (typeof COMMANDS)[number]

export const COMMAND_LABEL: Record<string, string> = {
  select: 'SELECT',
  insert: 'INSERT',
  update: 'UPDATE',
  delete: 'DELETE',
  merge: 'MERGE',
  copy: 'COPY',
  ddl: 'DDL',
  transaction: 'Transaction control',
  maintenance: 'Maintenance',
  utility: 'Utility (SET, SHOW, …)',
  other: 'Other',
}

const SLOT: Record<string, string> = {
  select: 'var(--s1)',
  insert: 'var(--s2)',
  update: 'var(--s3)',
  delete: 'var(--s4)',
  merge: 'var(--s5)',
  copy: 'var(--s6)',
  ddl: 'var(--s7)',
}

/** The chart group of a class: the seven colored classes, or "other". */
export function commandGroup(c: string): string {
  return SLOT[c] ? c : 'other'
}

export function commandColor(c: string): string {
  return SLOT[c] ?? 'var(--s-other)'
}

/** Groups in chart order (stack order follows the validated palette order). */
export const CHART_GROUPS = ['select', 'insert', 'update', 'delete', 'merge', 'copy', 'ddl', 'other']

export const GROUP_LABEL: Record<string, string> = { ...COMMAND_LABEL, other: 'Other (transaction, utility, …)' }

export function groupLabel(g: string): string {
  return GROUP_LABEL[g] ?? g
}

/** Resolves a CSS custom property to a concrete color (ECharts needs hex). */
export function cssVar(value: string): string {
  const name = /^var\((--[\w-]+)\)$/.exec(value)?.[1]
  if (!name || typeof window === 'undefined') return value
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || value
}
