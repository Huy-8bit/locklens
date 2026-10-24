import { useEffect, useId, useRef, type ReactNode } from 'react'
import { AlertTriangle, Loader2 } from 'lucide-react'
import { ApiError } from '../api/client'
import type { Severity, TargetState } from '../api/types'

export function cx(...parts: (string | false | null | undefined)[]): string {
  return parts.filter(Boolean).join(' ')
}

export function Card({ title, actions, children, className, id }: { title?: ReactNode; actions?: ReactNode; children: ReactNode; className?: string; id?: string }) {
  const headingId = useId()
  return (
    <section aria-labelledby={title ? headingId : undefined} id={id} className={cx('rounded-lg border border-border bg-panel', className)}>
      {(title || actions) && (
        <header className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-2.5">
          {title && <h2 id={headingId} className="text-sm font-semibold">{title}</h2>}
          {actions && <div className="flex items-center gap-2">{actions}</div>}
        </header>
      )}
      <div className="p-4">{children}</div>
    </section>
  )
}

const stateStyles: Record<TargetState, string> = {
  healthy: 'text-ok border-ok',
  degraded: 'text-warn border-warn',
  unreachable: 'text-crit border-crit',
  disabled: 'text-muted border-border',
  pending: 'text-muted border-border',
}

export function StateBadge({ state }: { state: TargetState }) {
  return (
    <span className={cx('inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium', stateStyles[state])}>
      <span aria-hidden className="mr-1.5 h-1.5 w-1.5 rounded-full bg-current" />
      {state}
    </span>
  )
}

const sevStyles: Record<Severity, string> = { critical: 'text-crit border-crit', warning: 'text-warn border-warn', info: 'text-info border-info' }

export function SeverityBadge({ severity }: { severity: Severity }) {
  return <span className={cx('inline-flex rounded-full border px-2 py-0.5 text-xs font-semibold', sevStyles[severity])}>{severity}</span>
}

export function Tag({ children, tone = 'muted' }: { children: ReactNode; tone?: 'muted' | 'warn' | 'crit' | 'ok' | 'info' }) {
  const tones = { muted: 'border-border text-muted', warn: 'border-warn text-warn', crit: 'border-crit text-crit', ok: 'border-ok text-ok', info: 'border-info text-info' }
  return <span className={cx('inline-flex rounded border px-1.5 py-0.5 text-xs', tones[tone])}>{children}</span>
}

type ButtonProps = React.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: 'primary' | 'secondary' | 'danger' | 'ghost'; busy?: boolean }

export function Button({ variant = 'secondary', busy, className, children, disabled, ...rest }: ButtonProps) {
  const styles = {
    primary: 'bg-accent text-accentfg border-accent hover:opacity-90',
    secondary: 'bg-panel text-fg border-border hover:bg-panel2',
    danger: 'bg-panel text-crit border-crit hover:bg-panel2',
    ghost: 'bg-transparent text-fg border-transparent hover:bg-panel2',
  }
  return (
    <button
      type="button"
      {...rest}
      disabled={disabled || busy}
      aria-busy={busy || undefined}
      className={cx('inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm font-medium disabled:cursor-not-allowed disabled:opacity-50', styles[variant], className)}
    >
      {busy && <Loader2 aria-hidden className="h-4 w-4 animate-spin" />}
      {children}
    </button>
  )
}

export function Spinner({ label = 'Loading' }: { label?: string }) {
  return (
    <div role="status" className="flex items-center gap-2 text-sm text-muted">
      <Loader2 aria-hidden className="h-4 w-4 animate-spin" />
      {label}…
    </div>
  )
}

export function ErrorBox({ error, title = 'Could not load data' }: { error: unknown; title?: string }) {
  const msg = error instanceof ApiError ? error.message : error instanceof Error ? error.message : 'Unexpected error'
  const rid = error instanceof ApiError && error.requestId ? ` (request ${error.requestId})` : ''
  return (
    <div role="alert" className="flex items-start gap-2 rounded-md border border-crit p-3 text-sm">
      <AlertTriangle aria-hidden className="mt-0.5 h-4 w-4 shrink-0 text-crit" />
      <div>
        <p className="font-medium text-crit">{title}</p>
        <p className="text-muted">
          {msg}
          {rid}
        </p>
      </div>
    </div>
  )
}

export function Empty({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="rounded-md border border-dashed border-border p-6 text-center text-sm">
      <p className="font-medium">{title}</p>
      {children && <div className="mt-1 text-muted">{children}</div>}
    </div>
  )
}

export function Notice({ tone = 'warn', children }: { tone?: 'warn' | 'info' | 'crit'; children: ReactNode }) {
  const tones = { warn: 'border-warn', info: 'border-info', crit: 'border-crit' }
  return <div className={cx('rounded-md border-l-4 bg-panel2 px-3 py-2 text-sm', tones[tone])}>{children}</div>
}

/** Renders loading and error states, then the children with data. */
export function DataState<T>({ query, children, loadingLabel }: { query: { data: T | undefined; isLoading: boolean; error: unknown }; children: (data: T) => ReactNode; loadingLabel?: string }) {
  if (query.isLoading) return <Spinner label={loadingLabel} />
  if (query.error && !query.data) return <ErrorBox error={query.error} />
  if (query.data === undefined) return <Spinner label={loadingLabel} />
  return <>{children(query.data)}</>
}

export function Pagination({ page, pageSize, total, onPage }: { page: number; pageSize: number; total: number; onPage: (p: number) => void }) {
  const pages = Math.max(1, Math.ceil(total / pageSize))
  return (
    <nav aria-label="Pagination" className="flex items-center justify-between gap-2 pt-3 text-sm">
      <span className="text-muted">
        {total === 0 ? 'No rows' : `Rows ${(page - 1) * pageSize + 1}–${Math.min(page * pageSize, total)} of ${total}`}
      </span>
      <div className="flex gap-2">
        <Button onClick={() => onPage(page - 1)} disabled={page <= 1}>
          Previous
        </Button>
        <span className="self-center tabular text-muted">
          Page {page} of {pages}
        </span>
        <Button onClick={() => onPage(page + 1)} disabled={page >= pages}>
          Next
        </Button>
      </div>
    </nav>
  )
}

/** Accessible modal confirmation built on the native dialog element. */
export function ConfirmDialog({ open, title, children, confirmLabel, onConfirm, onCancel, busy, danger }: {
  open: boolean
  title: string
  children: ReactNode
  confirmLabel: string
  onConfirm: () => void
  onCancel: () => void
  busy?: boolean
  danger?: boolean
}) {
  const ref = useRef<HTMLDialogElement>(null)
  const titleId = useId()
  useEffect(() => {
    const d = ref.current
    if (!d) return
    if (open && !d.open) d.showModal?.()
    if (!open && d.open) d.close?.()
  }, [open])
  return (
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      onCancel={(e) => {
        e.preventDefault()
        onCancel()
      }}
      className="m-auto w-[min(32rem,92vw)] rounded-lg border border-border bg-panel p-0 text-fg backdrop:bg-black/50"
    >
      {open && (
        <div className="p-5">
          <h2 id={titleId} className="text-base font-semibold">
            {title}
          </h2>
          <div className="mt-3 text-sm">{children}</div>
          <div className="mt-5 flex justify-end gap-2">
            <Button onClick={onCancel}>Cancel</Button>
            <Button variant={danger ? 'danger' : 'primary'} onClick={onConfirm} busy={busy}>
              {confirmLabel}
            </Button>
          </div>
        </div>
      )}
    </dialog>
  )
}

export function KeyValue({ items }: { items: [string, ReactNode][] }) {
  return (
    <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm">
      {items.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-muted">{k}</dt>
          <dd className="min-w-0 break-words">{v}</dd>
        </div>
      ))}
    </dl>
  )
}

export function PageHeader({ title, subtitle, actions }: { title: ReactNode; subtitle?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
      <div className="min-w-0">
        <h1 className="truncate text-xl font-semibold">{title}</h1>
        {subtitle && <p className="mt-0.5 text-sm text-muted">{subtitle}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}
