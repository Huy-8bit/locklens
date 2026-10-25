import { Link } from 'react-router'
import { Database } from 'lucide-react'
import type { FleetLiveTarget } from '../../api/types'
import { bytes, seconds } from '../../lib/format'
import { topology, type TopologyEdge } from '../../lib/fleet'
import { cx } from '../ui'

type Health = 'healthy' | 'warning' | 'critical' | 'unknown'

/** Lag health: time lag when PostgreSQL reports it, otherwise bytes behind. */
export function lagHealth(e: Pick<TopologyEdge, 'lagBytes' | 'lagSeconds' | 'state'>): Health {
  if (e.state && e.state !== 'streaming') return 'critical'
  if (e.lagSeconds !== null) return e.lagSeconds < 1 ? 'healthy' : e.lagSeconds < 30 ? 'warning' : 'critical'
  if (e.lagBytes !== null) return e.lagBytes < 16 * 1024 * 1024 ? 'healthy' : e.lagBytes < 256 * 1024 * 1024 ? 'warning' : 'critical'
  return 'unknown'
}

export function lagText(e: Pick<TopologyEdge, 'lagBytes' | 'lagSeconds'>): string {
  if (e.lagSeconds !== null) return seconds(e.lagSeconds)
  if (e.lagBytes !== null) return bytes(e.lagBytes)
  return '—'
}

const DOT: Record<Health, string> = { healthy: 'var(--good-mark)', warning: 'var(--warn-mark)', critical: 'var(--crit-mark)', unknown: 'var(--muted)' }
const TEXT: Record<Health, string> = { healthy: 'text-good', warning: 'text-warn', critical: 'text-crit', unknown: 'text-muted' }

function Node({ t, role, sub }: { t?: FleetLiveTarget; role: string; sub: string }) {
  const name = t?.target.name
  const body = (
    <span className="flex min-w-0 items-center gap-2.5">
      <span aria-hidden className="relative flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-accent-soft text-accent">
        <Database className="h-5 w-5" />
        {t && <span className="absolute -top-0.5 -right-0.5 h-2.5 w-2.5 rounded-full ring-2 ring-surface" style={{ background: t.target.status.state === 'healthy' ? 'var(--good-mark)' : t.target.status.state === 'unreachable' ? 'var(--crit-mark)' : 'var(--warn-mark)' }} />}
      </span>
      <span className="min-w-0 leading-tight">
        <span className="block truncate text-sm font-semibold text-ink">{name ?? sub}</span>
        <span className="block text-xs text-ink-2">{role}</span>
        {name && <span className="block truncate text-xs text-muted" title={sub}>{sub}</span>}
      </span>
    </span>
  )
  return t ? (
    <Link to={`/databases/${t.target.id}`} className="block min-w-0 rounded-xl p-1 hover:bg-surface-2">
      {body}
    </Link>
  ) : (
    <div className="min-w-0 p-1">{body}</div>
  )
}

function LagPill({ e }: { e: TopologyEdge }) {
  const h = lagHealth(e)
  return (
    <span className="flex shrink-0 flex-col items-end leading-tight" title="Replay lag: time when PostgreSQL reports it, otherwise WAL not yet replayed">
      <span className="flex items-center gap-1.5 text-sm font-semibold text-ink">
        <span aria-hidden className="h-2 w-2 rounded-full" style={{ background: DOT[h] }} />
        {lagText(e)}
      </span>
      <span className={cx('text-xs', TEXT[h])}>{h === 'unknown' ? 'lag unknown' : h}</span>
    </span>
  )
}

/** Primary → standby graph with lag, from monitored replication state. */
export function TopologyGraph({ targets, compact = false }: { targets: FleetLiveTarget[]; compact?: boolean }) {
  const { groups, standalone } = topology(targets)
  if (groups.length === 0) {
    return (
      <div className="py-6 text-center text-sm text-muted">
        No replication observed in this environment.
        {standalone.length > 0 && <span className="block">{standalone.length} standalone instance(s).</span>}
      </div>
    )
  }
  return (
    <div className="space-y-5">
      {groups.slice(0, compact ? 2 : groups.length).map((g) => (
        <div key={g.primary.target.id} className={cx('grid items-center gap-x-5 gap-y-2', !compact && 'md:grid-cols-[minmax(0,14rem)_minmax(0,1fr)]')}>
          <Node t={g.primary} role="Primary" sub={`${g.primary.target.host}:${g.primary.target.port}`} />
          <ul className={cx('relative space-y-2 border-l-2 border-line-strong pl-5', compact ? 'ml-6' : 'ml-6 md:ml-0')} aria-label={`Standbys of ${g.primary.target.name}`}>
            {g.edges.length === 0 && <li className="text-sm text-muted">No standby connected.</li>}
            {g.edges.map((e, i) => (
              <li key={i} className="relative flex items-center justify-between gap-3 rounded-xl border border-line py-1 pr-3 pl-1">
                <span aria-hidden className="absolute top-1/2 -left-[22px] h-0.5 w-5 bg-line-strong" />
                <Node t={e.standby} role="Replica" sub={e.address ?? e.name} />
                <LagPill e={e} />
              </li>
            ))}
          </ul>
        </div>
      ))}
      {compact && groups.length > 2 && <p className="text-xs text-muted">+{groups.length - 2} more primaries</p>}
    </div>
  )
}
