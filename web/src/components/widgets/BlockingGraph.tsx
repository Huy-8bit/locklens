import type { ReactNode } from 'react'
import { Lock, ShieldCheck } from 'lucide-react'
import type { FleetLiveTarget, Report, SnapshotSession } from '../../api/types'
import { ageOf, seconds } from '../../lib/format'
import { describeWait } from '../BlockingTree'
import { Badge, cx } from '../ui'

interface Waiter {
  pid: number
  /** The session this one waits on (the root for direct waiters). */
  via: number
  depth: number
}

/** Every session waiting behind `root`, breadth first, each listed once. */
export function waitersOf(report: Report, root: number): Waiter[] {
  const nodes = new Map(report.analysis.nodes.map((n) => [n.id, n]))
  const seen = new Set([root])
  const out: Waiter[] = []
  let frontier = [{ pid: root, depth: 0 }]
  while (frontier.length) {
    const next: typeof frontier = []
    for (const f of frontier) {
      for (const c of nodes.get(f.pid)?.blocks ?? []) {
        if (seen.has(c)) continue
        seen.add(c)
        out.push({ pid: c, via: f.pid, depth: f.depth + 1 })
        next.push({ pid: c, depth: f.depth + 1 })
      }
    }
    frontier = next
  }
  return out
}

function SessionCard({ s, pid, now, tone, children }: { s?: SnapshotSession; pid: number; now: string; tone: 'crit' | 'warn'; children?: ReactNode }) {
  return (
    <div className={cx('min-w-0 rounded-xl border bg-surface px-3 py-2.5', tone === 'crit' ? 'border-crit/50' : 'border-warn/50')}>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
        <span className="font-mono font-semibold text-ink">{pid === 0 ? 'prepared transaction' : `PID ${pid}`}</span>
        {children}
      </div>
      <div className="mt-0.5 truncate text-xs text-muted">
        {[s?.user, s?.database, s?.application_name].filter(Boolean).join(' · ') || '—'}
        {s?.xact_start && ` · tx ${seconds(ageOf(s.xact_start, now))}`}
      </div>
      {s?.query && (
        <div className="mt-1 truncate font-mono text-xs text-ink-2" title={s.query}>
          {s.query}
        </div>
      )}
    </div>
  )
}

/**
 * Blocker → waiter cards from pg_blocking_pids(). A cycle is labelled as a
 * cycle; PostgreSQL resolves real deadlocks itself, so it is not called one.
 */
export function BlockingGraph({ targets, max }: { targets: FleetLiveTarget[]; max?: number }) {
  const groups = targets.flatMap((t) =>
    t.report
      ? t.report.analysis.components.flatMap((c) => (c.roots.length ? c.roots : c.entries.slice(0, 1)).map((root) => ({ t, report: t.report as Report, root, cyclic: c.cyclic })))
      : [],
  )
  if (groups.length === 0) {
    return (
      <div className="flex items-center gap-3 rounded-xl bg-good-soft px-4 py-3 text-sm text-good">
        <ShieldCheck aria-hidden className="h-5 w-5 shrink-0" />
        No session is waiting on another session's lock right now.
      </div>
    )
  }
  const shown = max ? groups.slice(0, max) : groups
  return (
    <div className="space-y-5">
      {shown.map(({ t, report, root, cyclic }) => {
        const now = report.snapshot.server_time
        const sessions = new Map(report.snapshot.sessions.map((s) => [s.pid, s]))
        const waiters = waitersOf(report, root)
        const rs = sessions.get(root)
        return (
          <div key={`${t.target.id}:${root}`} className="grid items-center gap-3 sm:grid-cols-[minmax(0,1fr)_2rem_minmax(0,1.2fr)]">
            <div>
              {targets.length > 1 && <p className="mb-1 text-xs font-medium text-muted">{t.target.name}</p>}
              <SessionCard s={rs} pid={root} now={now} tone="crit">
                <Badge tone="crit">
                  <Lock aria-hidden className="h-3 w-3" />
                  {cyclic ? 'in a cycle' : 'blocker'}
                </Badge>
                {rs?.state && <span className={cx('text-xs', rs.state.startsWith('idle in transaction') ? 'text-warn' : 'text-ink-2')}>{rs.state}</span>}
              </SessionCard>
              <p className="mt-1 text-xs text-ink-2">
                Blocking {waiters.length} session{waiters.length === 1 ? '' : 's'}
              </p>
            </div>
            <svg aria-hidden viewBox="0 0 32 16" className="hidden h-4 w-8 text-crit sm:block">
              <path d="M0 8h26m-6-5 6 5-6 5" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
            <ul className="space-y-2" aria-label={`Sessions waiting on ${root}`}>
              {waiters.slice(0, 6).map((w) => {
                const s = sessions.get(w.pid)
                const waited = ageOf(s?.waiting_locks[0]?.wait_start, now)
                const wait = describeWait(s)
                return (
                  <li key={w.pid}>
                    <SessionCard s={s} pid={w.pid} now={now} tone="warn">
                      <Badge tone="warn">waiting{waited !== null ? ` ${seconds(waited)}` : ''}</Badge>
                      {w.depth > 1 && <span className="text-xs text-muted">behind PID {w.via}</span>}
                    </SessionCard>
                    {wait && <p className="mt-0.5 truncate px-1 text-xs text-muted">for {wait}</p>}
                  </li>
                )
              })}
              {waiters.length > 6 && <li className="px-1 text-xs text-muted">+{waiters.length - 6} more</li>}
            </ul>
          </div>
        )
      })}
      {max && groups.length > max && <p className="text-xs text-muted">+{groups.length - max} more blocking group(s)</p>}
    </div>
  )
}
