import type { ReactNode } from 'react'
import { cx } from './ui'

/** A fixed-height value tile; unknown values say so instead of showing 0. */
export function MetricTile({ label, value, hint, tone, title }: { label: string; value: ReactNode; hint?: ReactNode; tone?: 'crit' | 'warn' | 'ok'; title?: string }) {
  const tones = { crit: 'text-crit', warn: 'text-warn', ok: 'text-ok' }
  return (
    <div className="flex h-[88px] flex-col justify-between rounded-lg border border-border bg-panel px-3 py-2.5" title={title}>
      <div className="truncate text-xs font-medium uppercase tracking-wide text-muted">{label}</div>
      <div className={cx('tabular truncate text-xl font-semibold', tone && tones[tone])}>{value}</div>
      <div className="truncate text-xs text-muted">{hint ?? ' '}</div>
    </div>
  )
}
