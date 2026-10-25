import { Link } from 'react-router'
import { Plus, Server } from 'lucide-react'
import { useFleet } from '../api/hooks'
import type { FleetEntry } from '../api/types'
import { PageHeader } from '../components/Shell'
import { Badge, Card, Empty, Load, Meter, StateBadge } from '../components/ui'
import { ago, bytes, num, pct, rate } from '../lib/format'
import { useScope } from '../lib/scope'

function InstanceCard({ e }: { e: FleetEntry }) {
  const t = e.target
  const v = (k: string) => e.latest[k]?.value ?? null
  const used = v('connections.used')
  const max = v('connections.max')
  const usage = v('connections.usage_ratio')
  const commits = v('xact.commit.per_s')
  const rollbacks = v('xact.rollback.per_s')
  const blocked = v('blocking.blocked_sessions')
  return (
    <Link to={`/databases/${t.id}`} className="block rounded-xl border border-line bg-surface p-4 transition-colors hover:border-accent">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <h2 className="truncate font-semibold">{t.name}</h2>
          <p className="truncate text-xs text-muted">
            {t.host}:{t.port}/{t.database}
            {t.status.server_version ? ` · PostgreSQL ${t.status.server_version}` : ''}
            {t.status.is_standby ? ' · standby' : t.status.is_standby === false ? ' · primary' : ''}
          </p>
        </div>
        <StateBadge state={t.status.state} />
      </div>
      <dl className="mt-3 grid grid-cols-2 gap-x-4 gap-y-2 text-sm sm:grid-cols-4">
        <div>
          <dt className="text-xs text-muted">Connections</dt>
          <dd className="num font-semibold">
            {num(used)}
            <span className="font-normal text-muted"> / {num(max)}</span>
            <Meter value={used} max={max} tone={usage !== null && usage >= 0.8 ? 'warn' : undefined} />
          </dd>
        </div>
        <div>
          <dt className="text-xs text-muted">Queries/s</dt>
          <dd className="num font-semibold">{rate(v('statements.calls.per_s'), '')}</dd>
        </div>
        <div>
          <dt className="text-xs text-muted">Transactions/s</dt>
          <dd className="num font-semibold">{rate(commits !== null && rollbacks !== null ? commits + rollbacks : null, '')}</dd>
        </div>
        <div>
          <dt className="text-xs text-muted">Blocked</dt>
          <dd className={`num font-semibold ${blocked ? 'text-crit' : ''}`}>{num(blocked)}</dd>
        </div>
      </dl>
      <div className="mt-3 flex flex-wrap items-center gap-2 text-xs text-muted">
        <span>cache {pct(v('cache.hit_ratio'), 1)}</span>
        <span>· size {bytes(v('db.total_size_bytes'))}</span>
        <span>· updated {ago(t.status.last_success_at)}</span>
        {e.counts.open_incidents > 0 && <Badge tone="crit">{e.counts.open_incidents} open incident(s)</Badge>}
        {e.counts.critical_findings + e.counts.warning_findings > 0 && <Badge tone="warn">{e.counts.critical_findings + e.counts.warning_findings} finding(s)</Badge>}
      </div>
      {t.status.state === 'unreachable' && t.status.last_error && <p className="mt-2 line-clamp-2 text-xs text-crit">{t.status.last_error}</p>}
    </Link>
  )
}

export default function InstancesPage() {
  const fleet = useFleet()
  const { env } = useScope()
  return (
    <>
      <PageHeader
        title="Databases"
        subtitle={`Every PostgreSQL server LockLens monitors${env ? ` in ${env}` : ''}, as of the latest collection.`}
        actions={
          <Link to="/settings/targets/new" className="inline-flex items-center gap-1.5 rounded-lg border border-accent-fill bg-accent-fill px-3 py-1.5 text-sm font-medium text-accent-ink">
            <Plus aria-hidden className="h-4 w-4" /> Add instance
          </Link>
        }
      />
      <Load q={fleet} label="Loading instances">
        {(all) => {
          const d = { targets: all.targets.filter((e) => !env || e.target.environment === env) }
          return d.targets.length === 0 ? (
            <Card>
              <Empty title={env ? `No instance in “${env}”` : 'No instances yet'} icon={<Server aria-hidden className="h-6 w-6" />}>
                Add a PostgreSQL server with a read-only monitoring role (pg_read_all_stats).{' '}
                <Link className="text-accent underline" to="/settings/targets/new">
                  Add instance
                </Link>
              </Empty>
            </Card>
          ) : (
            <div className="grid gap-3 md:grid-cols-2 2xl:grid-cols-3">
              {d.targets.map((e) => (
                <InstanceCard key={e.target.id} e={e} />
              ))}
            </div>
          )
        }}
      </Load>
    </>
  )
}
