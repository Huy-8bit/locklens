import { Link } from 'react-router'
import { useFleet } from '../api/hooks'
import type { FleetEntry } from '../api/types'
import { DataState, Empty, PageHeader, SeverityBadge, StateBadge, Tag } from '../components/ui'
import { fmtAgo, fmtDuration, fmtNumber, fmtPercent } from '../lib/format'

function latest(e: FleetEntry, key: string): number | null {
  return e.latest[key]?.value ?? null
}

function TargetCard({ e }: { e: FleetEntry }) {
  const t = e.target
  const blocked = latest(e, 'blocking.blocked_sessions')
  const usage = latest(e, 'connections.usage_ratio')
  const oldest = latest(e, 'transactions.oldest_age_seconds')
  return (
    <li className="rounded-lg border border-border bg-panel">
      <Link to={`/targets/${t.id}`} className="block p-4 hover:bg-panel2">
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <h2 className="truncate font-semibold">{t.name}</h2>
            <p className="truncate text-xs text-muted">
              {t.host}:{t.port}/{t.database} · PostgreSQL {t.status.server_version ?? 'unknown'}
              {t.status.is_standby ? ' · standby' : t.status.is_standby === false ? ' · primary' : ''}
            </p>
          </div>
          <StateBadge state={t.status.state} />
        </div>
        <dl className="mt-3 grid grid-cols-3 gap-2 text-sm">
          <div>
            <dt className="text-xs text-muted">Blocked sessions</dt>
            <dd className={blocked ? 'font-semibold text-crit' : 'font-semibold'}>{fmtNumber(blocked)}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted">Connection usage</dt>
            <dd className="font-semibold">{fmtPercent(usage)}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted">Oldest transaction</dt>
            <dd className="font-semibold">{oldest === null && t.status.state === 'healthy' ? 'none open' : fmtDuration(oldest)}</dd>
          </div>
        </dl>
        <div className="mt-3 flex flex-wrap items-center gap-2 text-xs">
          {e.counts.open_incidents > 0 && <Tag tone="crit">{e.counts.open_incidents} open incident(s)</Tag>}
          {e.counts.critical_findings > 0 && <SeverityBadge severity="critical" />}
          {e.counts.warning_findings > 0 && <Tag tone="warn">{e.counts.warning_findings} warning finding(s)</Tag>}
          <span className="ml-auto text-muted">last collected {fmtAgo(t.status.last_success_at)}</span>
        </div>
        {t.status.state === 'unreachable' && t.status.last_error && <p className="mt-2 line-clamp-2 text-xs text-crit">{t.status.last_error}</p>}
      </Link>
    </li>
  )
}

export function FleetPage() {
  const q = useFleet()
  return (
    <>
      <PageHeader title="Fleet overview" subtitle="What is wrong right now across monitored PostgreSQL instances." />
      <DataState query={q} loadingLabel="Loading targets">
        {(d) =>
          d.targets.length === 0 ? (
            <Empty title="No targets yet">
              Add a PostgreSQL instance under <Link className="text-accent underline" to="/settings/targets/new">Targets & settings</Link>.
            </Empty>
          ) : (
            <ul className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
              {d.targets.map((e) => (
                <TargetCard key={e.target.id} e={e} />
              ))}
            </ul>
          )
        }
      </DataState>
    </>
  )
}
