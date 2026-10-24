import { useMemo, type ReactNode } from 'react'
import type { Report, ReportNode, SessionAges, SnapshotSession } from '../api/types'
import { fmtDuration } from '../lib/format'
import { describeLock } from '../lib/locks'
import { cx, Tag } from './ui'

interface Ctx {
  nodes: Map<number, ReportNode>
  sessions: Map<number, SnapshotSession>
  ages: Map<number, SessionAges>
  selected: number | null
  onSelect: (pid: number) => void
}

function label(pid: number, n?: ReportNode): string {
  if (pid === 0 || n?.kind === 'prepared_transaction') return 'prepared transaction (PID 0)'
  return `pid ${pid}`
}

function NodeLine({ pid, ctx, note }: { pid: number; ctx: Ctx; note?: string }) {
  const n = ctx.nodes.get(pid)
  const s = ctx.sessions.get(pid)
  const a = ctx.ages.get(pid)
  const blocked = (n?.blocked_by.length ?? 0) > 0
  return (
    <button
      type="button"
      onClick={() => ctx.onSelect(pid)}
      aria-pressed={ctx.selected === pid}
      className={cx(
        'flex w-full flex-wrap items-center gap-x-3 gap-y-1 rounded-md border px-2.5 py-1.5 text-left text-sm',
        ctx.selected === pid ? 'border-accent bg-panel2' : 'border-transparent hover:bg-panel2',
      )}
    >
      <span className="font-mono font-semibold">{label(pid, n)}</span>
      {n?.root && <Tag tone="crit">root blocker</Tag>}
      {n?.in_cycle && <Tag tone="warn">cycle</Tag>}
      {n?.kind === 'missing' && <Tag tone="warn">no metadata in this collection</Tag>}
      {note && <span className="text-xs italic text-muted">({note})</span>}
      {s && (
        <>
          {!s.details_visible && <Tag tone="warn">details hidden</Tag>}
          <span className={cx(s.state?.startsWith('idle in transaction') && 'font-medium text-warn')}>{s.state ?? 'state unknown'}</span>
          <span className="text-muted">tx {fmtDuration(a?.transaction_age_seconds)}</span>
          {blocked && <span className="text-muted">lock wait {fmtDuration(a?.lock_wait_seconds)}</span>}
          {s.application_name && <span className="truncate text-muted">app {s.application_name}</span>}
        </>
      )}
      {n && n.direct_blocked_count > 0 && (
        <span className="ml-auto tabular text-muted">
          blocks {n.direct_blocked_count} directly · {n.transitive_blocked_count} total
        </span>
      )}
    </button>
  )
}

function Subtree({ pid, ctx, shown, path }: { pid: number; ctx: Ctx; shown: Set<number>; path: Set<number> }): ReactNode {
  if (path.has(pid)) return <li><NodeLine pid={pid} ctx={ctx} note="cycle back to an ancestor" /></li>
  if (shown.has(pid)) return <li><NodeLine pid={pid} ctx={ctx} note="shown above" /></li>
  shown.add(pid)
  const nextPath = new Set(path).add(pid)
  const n = ctx.nodes.get(pid)
  const s = ctx.sessions.get(pid)
  return (
    <li>
      <NodeLine pid={pid} ctx={ctx} />
      {s?.waiting_locks.map((l, i) => (
        <p key={i} className="ml-3 text-xs text-muted">
          waiting for {describeLock(l)}
        </p>
      ))}
      {n && n.blocks.length > 0 && (
        <ul className="ml-4 border-l border-dashed border-border pl-3">
          {n.blocks.map((c) => (
            <Subtree key={c} pid={c} ctx={ctx} shown={shown} path={nextPath} />
          ))}
        </ul>
      )}
    </li>
  )
}

/**
 * Renders blocking incidents as trees (blocker → waiter). Shared descendants
 * are printed once and referenced afterwards; cycles stop at the repeated node.
 */
export function BlockingTree({ report, selected, onSelect }: { report: Report; selected: number | null; onSelect: (pid: number) => void }) {
  const ctx = useMemo<Ctx>(
    () => ({
      nodes: new Map(report.analysis.nodes.map((n) => [n.id, n])),
      sessions: new Map(report.snapshot.sessions.map((s) => [s.pid, s])),
      ages: new Map(report.analysis.sessions.map((a) => [a.pid, a])),
      selected,
      onSelect,
    }),
    [report, selected, onSelect],
  )
  if (report.analysis.components.length === 0) {
    return <p className="text-sm text-muted">No blocking detected: no session is waiting for a lock held by another session.</p>
  }
  return (
    <div className="space-y-4">
      {report.analysis.components.map((c) => {
        const starts = c.cyclic ? (c.entries.length ? c.entries : c.cycle_members.slice(0, 1)) : c.roots
        const shown = new Set<number>()
        return (
          <div key={c.id} className="rounded-md border border-border p-2">
            <p className="mb-1 px-1 text-xs font-medium uppercase tracking-wide text-muted">
              Incident {c.id}: {c.cyclic ? `cycle among pids ${c.cycle_members.join(', ')} (not a confirmed deadlock)` : `${c.members.length} sessions`}
            </p>
            <ul aria-label={`Blocking tree ${c.id}`}>
              {starts.map((s) => (
                <Subtree key={s} pid={s} ctx={ctx} shown={shown} path={new Set()} />
              ))}
            </ul>
          </div>
        )
      })}
    </div>
  )
}
