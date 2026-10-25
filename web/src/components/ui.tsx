import { useEffect, useId, useRef, type ButtonHTMLAttributes, type ReactNode } from 'react'
import { AlertTriangle, CircleCheck, CircleHelp, Loader2, OctagonAlert, X } from 'lucide-react'
import type { UseQueryResult } from '@tanstack/react-query'
import { Link } from 'react-router'
import { ApiError } from '../api/client'
import type { Severity, TargetState } from '../api/types'

export function cx(...c: (string | false | null | undefined)[]): string {
  return c.filter(Boolean).join(' ')
}

export function Card({ title, subtitle, actions, children, className, pad = true }: {
  title?: ReactNode
  subtitle?: ReactNode
  actions?: ReactNode
  children: ReactNode
  className?: string
  pad?: boolean
}) {
  const id = useId()
  return (
    <section aria-labelledby={title ? id : undefined} className={cx('rounded-2xl border border-line bg-surface shadow-card', className)}>
      {(title || actions) && (
        <header className="flex items-start justify-between gap-3 px-5 pt-4 pb-2.5">
          <div className="min-w-0">
            {title && <h2 id={id} className="text-[16px] font-semibold tracking-tight text-ink">{title}</h2>}
            {subtitle && <p className="mt-0.5 text-xs text-muted">{subtitle}</p>}
          </div>
          {actions && <div className="flex shrink-0 flex-wrap items-center justify-end gap-2">{actions}</div>}
        </header>
      )}
      <div className={pad ? 'px-5 pb-5' : ''}>{children}</div>
    </section>
  )
}

/** A KPI tile: label, a large value, and context. Unknown values say so. */
export function Stat({ label, value, sub, tone, icon }: { label: string; value: ReactNode; sub?: ReactNode; tone?: 'good' | 'warn' | 'crit'; icon?: ReactNode }) {
  const tones = { good: 'text-good', warn: 'text-warn', crit: 'text-crit' }
  return (
    <div className="rounded-xl border border-line bg-surface px-4 py-3">
      <div className="flex items-center gap-1.5 text-xs font-medium text-muted">
        {icon}
        <span className="truncate">{label}</span>
      </div>
      <div className={cx('mt-1 truncate text-2xl font-semibold tracking-tight', tone ? tones[tone] : 'text-ink')}>{value}</div>
      {sub && <div className="mt-0.5 truncate text-xs text-muted">{sub}</div>}
    </div>
  )
}

const TILE = {
  blue: 'bg-accent-soft text-accent',
  violet: 'bg-violet-soft text-violet',
  amber: 'bg-warn-soft text-warn',
  green: 'bg-good-soft text-good',
  red: 'bg-crit-soft text-crit',
}

/**
 * Trend label: the change from the start of the range. `upIsGood` decides
 * the color (neutral when a direction is neither good nor bad); the arrow
 * and the number carry the meaning without color.
 */
export function Delta({ from, to, upIsGood }: { from: number | null | undefined; to: number | null | undefined; upIsGood?: boolean }) {
  if (from == null || to == null || !Number.isFinite(from) || !Number.isFinite(to)) return null
  if (from === 0 && to === 0) return <span className="rounded-md bg-surface-3 px-1.5 py-0.5 text-xs font-semibold text-ink-2">0%</span>
  if (from === 0) return null
  const pctChange = ((to - from) / Math.abs(from)) * 100
  if (Math.abs(pctChange) < 0.5) return <span className="rounded-md bg-surface-3 px-1.5 py-0.5 text-xs font-semibold text-ink-2">0%</span>
  const up = pctChange > 0
  const tone = upIsGood === undefined ? 'bg-surface-3 text-ink-2' : up === upIsGood ? 'bg-good-soft text-good' : 'bg-crit-soft text-crit'
  return (
    <span title="Change since the start of the time range" className={cx('inline-flex items-center gap-0.5 rounded-md px-1.5 py-0.5 text-xs font-semibold', tone)}>
      <span aria-hidden>{up ? '↑' : '↓'}</span>
      <span className="sr-only">{up ? 'up' : 'down'}</span>
      {Math.abs(pctChange) >= 100 ? Math.round(Math.abs(pctChange)) : Math.abs(pctChange).toFixed(Math.abs(pctChange) < 10 ? 1 : 0)}%
    </span>
  )
}

/** KPI card with a colored icon tile, a large value, a trend, and context. */
export function KpiCard({ icon, tone, label, value, unit, delta, sub, to }: {
  icon: ReactNode
  tone: keyof typeof TILE
  label: string
  value: ReactNode
  unit?: string
  delta?: ReactNode
  sub?: ReactNode
  /** Makes the whole card a link to the detail page. */
  to?: string
}) {
  const body = (
    <>
      <span aria-hidden className={cx('flex h-11 w-11 shrink-0 items-center justify-center rounded-full [&>svg]:h-5 [&>svg]:w-5', TILE[tone])}>{icon}</span>
      <div className="min-w-0 flex-1">
        <div className="text-[13px] font-medium leading-snug text-ink-2">{label}</div>
        <div className="mt-0.5 flex flex-wrap items-baseline gap-x-2 gap-y-1">
          <span className="text-[26px] font-bold leading-tight tracking-tight text-ink">
            {value}
            {unit && <span className="ml-1 text-lg font-semibold">{unit}</span>}
          </span>
          {delta}
        </div>
        {sub && <div className="mt-0.5 text-xs leading-snug text-muted">{sub}</div>}
      </div>
    </>
  )
  const cls = 'flex items-start gap-3 rounded-2xl border border-line bg-surface p-4 shadow-card'
  return to ? (
    <Link to={to} className={cx(cls, 'transition-colors hover:border-line-strong')}>
      {body}
    </Link>
  ) : (
    <div className={cls}>{body}</div>
  )
}

/** Pill tabs used inside cards (Total time / Calls / …). */
export function Pills<T extends string>({ value, onChange, items, label }: { value: T; onChange: (v: T) => void; items: { value: T; label: string }[]; label: string }) {
  return (
    <div role="radiogroup" aria-label={label} className="inline-flex flex-wrap gap-1 rounded-xl bg-surface-3 p-1">
      {items.map((it) => (
        <button
          key={it.value}
          type="button"
          role="radio"
          aria-checked={value === it.value}
          onClick={() => onChange(it.value)}
          className={cx('rounded-lg px-3 py-1.5 text-xs font-semibold', value === it.value ? 'bg-accent-fill text-accent-ink shadow-sm' : 'text-ink-2 hover:text-ink')}
        >
          {it.label}
        </button>
      ))}
    </div>
  )
}

/** A horizontal bar showing part of a whole (e.g. connections used / max). */
export function Meter({ value, max, tone }: { value: number | null | undefined; max: number | null | undefined; tone?: 'good' | 'warn' | 'crit' }) {
  const ratio = value != null && max ? Math.min(1, value / max) : null
  const color = tone === 'crit' ? 'var(--crit-mark)' : tone === 'warn' ? 'var(--warn-mark)' : 'var(--accent)'
  return (
    <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-surface-3" role="presentation">
      {ratio !== null && <div className="h-full rounded-full" style={{ width: `${ratio * 100}%`, background: color }} />}
    </div>
  )
}

export function Button({ variant = 'default', size = 'md', busy, className, children, ...rest }: ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: 'default' | 'primary' | 'danger' | 'ghost'
  size?: 'sm' | 'md'
  busy?: boolean
}) {
  const v = {
    default: 'border border-line-strong bg-surface hover:bg-surface-2 text-ink',
    primary: 'border border-accent-fill bg-accent-fill text-accent-ink hover:opacity-90',
    danger: 'border border-crit text-crit hover:bg-surface-2',
    ghost: 'text-ink-2 hover:bg-surface-2',
  }[variant]
  return (
    <button
      type="button"
      {...rest}
      disabled={busy || rest.disabled}
      className={cx('inline-flex items-center justify-center gap-1.5 rounded-lg font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50', size === 'sm' ? 'px-2 py-1 text-xs' : 'px-3 py-1.5 text-sm', v, className)}
    >
      {busy && <Loader2 aria-hidden className="h-3.5 w-3.5 animate-spin" />}
      {children}
    </button>
  )
}

export function Badge({ children, tone = 'muted', title }: { children: ReactNode; tone?: 'muted' | 'good' | 'warn' | 'crit' | 'accent'; title?: string }) {
  const t = {
    muted: 'bg-surface-3 text-ink-2',
    good: 'bg-good-soft text-good',
    warn: 'bg-warn-soft text-warn',
    crit: 'bg-crit-soft text-crit',
    accent: 'bg-accent-soft text-accent',
  }[tone]
  return (
    <span title={title} className={cx('inline-flex items-center gap-1 whitespace-nowrap rounded-md px-2 py-0.5 text-[11.5px] font-semibold', t)}>
      {children}
    </span>
  )
}

const stateTone: Record<TargetState, 'good' | 'warn' | 'crit' | 'muted'> = { healthy: 'good', degraded: 'warn', unreachable: 'crit', pending: 'muted', disabled: 'muted' }

export function StateBadge({ state }: { state: TargetState }) {
  const Icon = state === 'healthy' ? CircleCheck : state === 'unreachable' ? OctagonAlert : state === 'degraded' ? AlertTriangle : CircleHelp
  return (
    <Badge tone={stateTone[state]}>
      <Icon aria-hidden className="h-3 w-3" />
      {state}
    </Badge>
  )
}

export function StateDot({ state }: { state: TargetState }) {
  const color = { healthy: 'var(--good-mark)', degraded: 'var(--warn-mark)', unreachable: 'var(--crit-mark)', pending: 'var(--muted)', disabled: 'var(--muted)' }[state]
  return <span aria-label={state} role="img" className="inline-block h-2 w-2 shrink-0 rounded-full" style={{ background: color }} />
}

export function SeverityBadge({ severity }: { severity: Severity }) {
  const tone = severity === 'critical' ? 'crit' : severity === 'warning' ? 'warn' : 'accent'
  const Icon = severity === 'critical' ? OctagonAlert : severity === 'warning' ? AlertTriangle : CircleHelp
  return (
    <Badge tone={tone}>
      <Icon aria-hidden className="h-3 w-3" />
      {severity}
    </Badge>
  )
}

export function Spinner({ label = 'Loading' }: { label?: string }) {
  return (
    <div role="status" className="flex items-center gap-2 py-6 text-sm text-muted">
      <Loader2 aria-hidden className="h-4 w-4 animate-spin" />
      {label}…
    </div>
  )
}

export function Empty({ title, children, icon }: { title: string; children?: ReactNode; icon?: ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center rounded-lg border border-dashed border-line-strong px-4 py-8 text-center">
      {icon && <div className="mb-2 text-muted">{icon}</div>}
      <p className="text-sm font-medium text-ink">{title}</p>
      {children && <div className="mt-1 max-w-md text-xs text-muted">{children}</div>}
    </div>
  )
}

export function ErrorBox({ error, title = 'Something went wrong' }: { error: unknown; title?: string }) {
  const msg = error instanceof Error ? error.message : String(error)
  const rid = error instanceof ApiError && error.requestId ? error.requestId : ''
  return (
    <div role="alert" className="rounded-lg border border-crit/40 bg-surface px-3 py-2.5 text-sm">
      <p className="font-medium text-crit">{title}</p>
      <p className="mt-0.5 text-ink-2">{msg}</p>
      {rid && <p className="mt-1 text-xs text-muted">Request ID {rid}</p>}
    </div>
  )
}

export function Notice({ tone = 'info', children }: { tone?: 'info' | 'warn' | 'crit'; children: ReactNode }) {
  const t = { info: 'border-accent/30', warn: 'border-warn/50', crit: 'border-crit/50' }[tone]
  const Icon = tone === 'info' ? CircleHelp : tone === 'warn' ? AlertTriangle : OctagonAlert
  const ic = { info: 'text-accent', warn: 'text-warn', crit: 'text-crit' }[tone]
  return (
    <div className={cx('flex gap-2 rounded-lg border bg-surface px-3 py-2 text-sm text-ink-2', t)}>
      <Icon aria-hidden className={cx('mt-0.5 h-4 w-4 shrink-0', ic)} />
      <div className="min-w-0">{children}</div>
    </div>
  )
}

/** Renders loading and error states of a query, then its data. */
export function Load<T>({ q, children, label }: { q: UseQueryResult<T>; children: (d: T) => ReactNode; label?: string }) {
  if (q.data !== undefined) return <>{children(q.data)}</>
  if (q.error) return <ErrorBox error={q.error} title="Could not load data" />
  return <Spinner label={label} />
}

export function Tabs<T extends string>({ value, onChange, items, label }: { value: T; onChange: (v: T) => void; items: { value: T; label: ReactNode; count?: number }[]; label: string }) {
  return (
    <div role="tablist" aria-label={label} className="flex gap-1 overflow-x-auto border-b border-line">
      {items.map((it) => (
        <button
          key={it.value}
          role="tab"
          type="button"
          aria-selected={value === it.value}
          onClick={() => onChange(it.value)}
          className={cx('-mb-px whitespace-nowrap border-b-2 px-3 py-2 text-sm font-medium', value === it.value ? 'border-accent text-ink' : 'border-transparent text-muted hover:text-ink')}
        >
          {it.label}
          {it.count !== undefined && <span className="ml-1.5 rounded bg-surface-3 px-1.5 text-xs text-ink-2">{it.count}</span>}
        </button>
      ))}
    </div>
  )
}

export function Segmented<T extends string>({ value, onChange, items, label }: { value: T; onChange: (v: T) => void; items: { value: T; label: string }[]; label: string }) {
  return (
    <div role="radiogroup" aria-label={label} className="inline-flex rounded-lg border border-line-strong bg-surface p-0.5">
      {items.map((it) => (
        <button
          key={it.value}
          type="button"
          role="radio"
          aria-checked={value === it.value}
          onClick={() => onChange(it.value)}
          className={cx('rounded-md px-2.5 py-1 text-xs font-medium', value === it.value ? 'bg-accent-soft text-accent' : 'text-ink-2 hover:text-ink')}
        >
          {it.label}
        </button>
      ))}
    </div>
  )
}

export function Field({ label, hint, error, children }: { label: string; hint?: ReactNode; error?: string; children: ReactNode }) {
  return (
    <label className="block text-sm">
      <span className="mb-1 block font-medium text-ink">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-xs text-muted">{hint}</span>}
      {error && <span className="mt-1 block text-xs text-crit">{error}</span>}
    </label>
  )
}

export const inputCls = 'w-full rounded-lg border border-line-strong bg-surface px-2.5 py-1.5 text-sm text-ink placeholder:text-muted'
/** A select sized to its content (inputCls is full width). */
export const selectCls = 'max-w-full rounded-lg border border-line-strong bg-surface px-2.5 py-1.5 text-sm text-ink'

/** Right-side panel for details; Escape closes it and focus returns. */
export function Drawer({ open, title, onClose, children }: { open: boolean; title: ReactNode; onClose: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const prev = document.activeElement as HTMLElement | null
    ref.current?.focus()
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('keydown', onKey)
      prev?.focus()
    }
  }, [open, onClose])
  if (!open) return null
  return (
    <div className="fixed inset-0 z-40 flex justify-end bg-black/30" onClick={onClose}>
      <div ref={ref} tabIndex={-1} role="dialog" aria-modal="true" aria-label={typeof title === 'string' ? title : 'Details'} onClick={(e) => e.stopPropagation()} className="flex h-full w-full max-w-xl flex-col border-l border-line bg-surface shadow-2xl outline-none">
        <div className="flex items-center justify-between gap-2 border-b border-line px-4 py-3">
          <h2 className="min-w-0 truncate text-sm font-semibold">{title}</h2>
          <Button variant="ghost" size="sm" onClick={onClose} aria-label="Close">
            <X aria-hidden className="h-4 w-4" />
          </Button>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto p-4">{children}</div>
      </div>
    </div>
  )
}

export function Confirm({ open, title, children, confirmLabel, danger, onConfirm, onCancel, busy }: {
  open: boolean
  title: string
  children: ReactNode
  confirmLabel: string
  danger?: boolean
  busy?: boolean
  onConfirm: () => void
  onCancel: () => void
}) {
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onCancel()
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onCancel])
  if (!open) return null
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
      <div role="alertdialog" aria-modal="true" aria-label={title} className="w-full max-w-md rounded-xl border border-line bg-surface p-5 shadow-2xl">
        <h2 className="text-base font-semibold">{title}</h2>
        <div className="mt-2 text-sm text-ink-2">{children}</div>
        <div className="mt-5 flex justify-end gap-2">
          <Button onClick={onCancel}>Cancel</Button>
          <Button variant={danger ? 'danger' : 'primary'} busy={busy} onClick={onConfirm} autoFocus>
            {confirmLabel}
          </Button>
        </div>
      </div>
    </div>
  )
}

export function KeyValue({ items }: { items: [ReactNode, ReactNode][] }) {
  return (
    <dl className="grid grid-cols-[minmax(0,auto)_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-sm">
      {items.map(([k, v], i) => (
        <div key={i} className="contents">
          <dt className="text-muted">{k}</dt>
          <dd className="min-w-0 break-words text-ink">{v}</dd>
        </div>
      ))}
    </dl>
  )
}
