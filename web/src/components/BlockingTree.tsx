import type { ReactNode } from 'react'
import type { Report, SnapshotSession } from '../api/types'
import { ageOf, seconds } from '../lib/format'
import { Badge } from './ui'

/** Describes what a waiting session waits for, from its waiting lock. */
export function describeWait(s: SnapshotSession | undefined): string | null {
  const l = s?.waiting_locks[0]
  if (!l) return null
  const what = l.relation ? `relation ${l.relation}` : l.transactionid ? `transaction ${l.transactionid} (a row lock)` : l.virtualxid ? `virtual transaction ${l.virtualxid}` : l.locktype
  return `${l.mode} on ${what}`
}

/**
 * Blocking trees from pg_blocking_pids(): each root blocker and the sessions
 * waiting behind it. Shared descendants are drawn once; cycles terminate and
 * are labelled as cycles, not as confirmed deadlocks.
 */
export function BlockingTree({ report, onSelect }: { report: Report; onSelect?: (pid: number) => void }) {
  const nodes = new Map(report.analysis.nodes.map((n) => [n.id, n]))
  const sessions = new Map(report.snapshot.sessions.map((s) => [s.pid, s]))
  const now = report.snapshot.server_time
  const drawn = new Set<number>()

  const render = (pid: number, depth: number, path: Set<number>): ReactNode => {
    const n = nodes.get(pid)
    const s = sessions.get(pid)
    const again = drawn.has(pid)
    drawn.add(pid)
    const cyc = path.has(pid)
    const wait = describeWait(s)
    return (
      <li key={`${pid}-${depth}`} className="mt-1.5">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-sm">
          <button type="button" onClick={() => pid > 0 && onSelect?.(pid)} className="font-mono font-semibold hover:underline">
            {pid === 0 ? 'prepared transaction' : `pid ${pid}`}
          </button>
          {n?.root && <Badge tone="crit">root blocker</Badge>}
          {s?.state && <span className={s.state.startsWith('idle in transaction') ? 'text-warn' : 'text-ink-2'}>{s.state}</span>}
          {s?.xact_start && <span className="text-xs text-muted">tx {seconds(ageOf(s.xact_start, now))}</span>}
          {s?.waiting_locks[0]?.wait_start && <span className="text-xs text-muted">waiting {seconds(ageOf(s.waiting_locks[0].wait_start, now))}</span>}
          {s?.application_name && <span className="text-xs text-muted">{s.application_name}</span>}
          {n && n.transitive_blocked_count > 0 && <span className="text-xs text-ink-2">blocks {n.direct_blocked_count} directly, {n.transitive_blocked_count} in total</span>}
          {(again || cyc) && <span className="text-xs text-muted">{cyc ? '(cycle — shown above)' : '(shown above)'}</span>}
        </div>
        {wait && <div className="text-xs text-muted">waiting for {wait}</div>}
        {s?.query && <div className="mt-0.5 max-w-3xl truncate font-mono text-xs text-ink-2" title={s.query}>{s.query}</div>}
        {!again && !cyc && n && n.blocks.length > 0 && (
          <ul className="ml-4 border-l border-line-strong pl-3">{n.blocks.map((c) => render(c, depth + 1, new Set([...path, pid])))}</ul>
        )}
      </li>
    )
  }

  return (
    <div className="space-y-4">
      {report.analysis.components.map((c) => (
        <div key={c.id}>
          <p className="text-xs font-medium uppercase tracking-wide text-muted">
            Blocking group {c.id} · {c.members.length} sessions{c.cyclic ? ' · contains a cycle (PostgreSQL resolves real deadlocks itself)' : ''}
          </p>
          <ul aria-label={`Blocking tree ${c.id}`}>{(c.roots.length ? c.roots : c.entries).map((r) => render(r, 0, new Set()))}</ul>
        </div>
      ))}
    </div>
  )
}
