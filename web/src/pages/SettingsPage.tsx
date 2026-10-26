import { useState, type ReactNode } from 'react'
import { Link, Navigate, NavLink, useNavigate, useParams } from 'react-router'
import { Bell, HardDrive, Plus, Server, SlidersHorizontal, Users } from 'lucide-react'
import { useDeleteTarget, useImportIncident, useStorage, useTargets } from '../api/hooks'
import type { Permission, Target } from '../api/types'
import { PageHeader } from '../components/Shell'
import { UsersCard } from '../components/UsersCard'
import { NotificationsCard, SilencesCard } from '../components/NotificationsCard'
import { useCan } from '../auth'
import { ThresholdsForm } from '../components/ThresholdsForm'
import { tableCls, tdCls, tdNum, Th, trCls } from '../components/Table'
import { Badge, Button, Card, Confirm, cx, Empty, ErrorBox, Load, StateBadge } from '../components/ui'
import { bytes, compact, seconds } from '../lib/format'

function Targets() {
  const q = useTargets()
  const del = useDeleteTarget()
  const [pending, setPending] = useState<{ t: Target; purge: boolean } | null>(null)
  return (
    <Card
      pad={false}
      subtitle="Connection strings are encrypted and never shown again."
      actions={
        <Link to="/settings/targets/new" className="inline-flex items-center gap-1.5 rounded-lg border border-accent-fill bg-accent-fill px-3 py-1.5 text-sm font-medium text-accent-ink">
          <Plus aria-hidden className="h-4 w-4" /> Add instance
        </Link>
      }
    >
      <Load q={q}>
        {(d) =>
          d.targets.length === 0 ? (
            <div className="px-4 pb-4">
              <Empty title="No instances" />
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className={tableCls}>
                <thead>
                  <tr>
                    <Th>Name</Th>
                    <Th>Server</Th>
                    <Th>State</Th>
                    <Th>Interval</Th>
                    <Th>SQL text</Th>
                    <Th>
                      <span className="sr-only">Actions</span>
                    </Th>
                  </tr>
                </thead>
                <tbody>
                  {d.targets.map((t) => (
                    <tr key={t.id} className={trCls}>
                      <td className={tdCls}>
                        <Link className="whitespace-nowrap font-medium hover:underline" to={`/databases/${t.id}`}>
                          {t.name}
                        </Link>
                      </td>
                      <td className={`${tdCls} text-xs text-ink-2`}>
                        {t.user}@{t.host}:{t.port}/{t.database}
                      </td>
                      <td className={tdCls}>
                        <StateBadge state={t.status.state} />
                      </td>
                      <td className={`${tdCls} text-xs`}>{seconds(t.interval_seconds)}</td>
                      <td className={tdCls}>{t.query_text_enabled ? <Badge tone="good">collected</Badge> : <Badge>off</Badge>}</td>
                      <td className={`${tdCls} text-right`}>
                        <div className="flex justify-end gap-1">
                          <Link to={`/settings/targets/${t.id}`} className="rounded-lg border border-line-strong px-2 py-1 text-xs hover:bg-surface-2">
                            Edit
                          </Link>
                          <Button size="sm" variant="danger" onClick={() => setPending({ t, purge: false })}>
                            Delete
                          </Button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )
        }
      </Load>
      <Confirm
        open={!!pending}
        danger
        title={`Delete ${pending?.t.name ?? ''}?`}
        confirmLabel="Delete"
        busy={del.isPending}
        onCancel={() => setPending(null)}
        onConfirm={() => pending && del.mutate({ id: pending.t.id, purge: pending.purge }, { onSuccess: () => setPending(null) })}
      >
        <p>Collection stops. Its history expires with normal retention unless you delete it now. Incidents are kept.</p>
        <label className="mt-3 flex items-center gap-2">
          <input type="checkbox" checked={pending?.purge ?? false} onChange={(e) => pending && setPending({ ...pending, purge: e.target.checked })} />
          Also delete its history now
        </label>
        {del.error && <ErrorBox error={del.error} />}
      </Confirm>
    </Card>
  )
}

function Storage() {
  const q = useStorage()
  const [all, setAll] = useState(false)
  return (
    <Card pad={false} title="Monitoring store" subtitle="LockLens' own database. Retention is set with LOCKLENS_RETENTION, LOCKLENS_INCIDENT_RETENTION, and LOCKLENS_FINDING_RETENTION.">
      <Load q={q}>
        {(d) => {
          const tables = d.tables ?? []
          // Older servers do not report it.
          const ts = d.timescale ?? { enabled: false, compression: false, reason: 'not reported by this server' }
          const before = tables.reduce((n, t) => n + (t.compressed_before_bytes ?? 0), 0)
          const after = tables.reduce((n, t) => n + (t.compressed_after_bytes ?? 0), 0)
          return (
            <div className="overflow-x-auto">
              <div className="px-5 pb-3 text-sm">
                {ts.enabled ? (
                  <p>
                    <Badge tone="good">TimescaleDB {ts.version}</Badge>{' '}
                    <span className="text-ink-2">
                      {ts.hypertables?.length ?? 0} history tables are hypertables (daily or weekly chunks; retention drops whole chunks).{' '}
                      {ts.compression
                        ? `Chunks older than ${seconds(ts.compress_after_seconds ?? 0)} are compressed${before > 0 ? `: ${bytes(before)} stored in ${bytes(after)} (${(before / Math.max(after, 1)).toFixed(1)}× smaller)` : ''}.`
                        : `Compression is off${ts.license === 'apache' ? ' (Apache-only build: compression needs the Community edition)' : ' (LOCKLENS_STORE_COMPRESS_AFTER=0)'}.`}
                    </span>
                  </p>
                ) : (
                  <p className="text-ink-2">
                    <Badge>PostgreSQL tables</Badge> {ts.reason}. With TimescaleDB, history is partitioned by time and compressed; see the deployment guide.
                  </p>
                )}
              </div>
              <table className={tableCls}>
                <caption className="sr-only">Monitoring store tables</caption>
                <thead>
                  <tr>
                    <Th>Table</Th>
                    <Th align="right">Size</Th>
                    <Th align="right">Rows (estimate)</Th>
                    {ts.compression && <Th align="right">Compression</Th>}
                  </tr>
                </thead>
                <tbody>
                  {(all ? tables : tables.slice(0, 8)).map((t) => (
                    <tr key={t.table} className={trCls}>
                      <td className={`${tdCls} font-mono text-xs`}>{t.table}</td>
                      <td className={tdNum}>{bytes(t.bytes)}</td>
                      <td className={tdNum}>{compact(t.estimated_rows)}</td>
                      {ts.compression && (
                        <td className={tdNum}>{t.compressed_before_bytes && t.compressed_after_bytes ? `${(t.compressed_before_bytes / t.compressed_after_bytes).toFixed(1)}×` : '—'}</td>
                      )}
                    </tr>
                  ))}
                  {tables.length > 8 && (
                    <tr>
                      <td colSpan={ts.compression ? 4 : 3} className="px-4 py-2">
                        <button type="button" onClick={() => setAll((a) => !a)} className="text-sm font-medium text-accent hover:underline">
                          {all ? 'Show the largest tables only' : `Show all ${tables.length} tables`}
                        </button>
                      </td>
                    </tr>
                  )}
                  <tr className="border-t border-line-strong font-medium">
                    <td className={tdCls}>Total</td>
                    <td className={tdNum}>{bytes(tables.reduce((n, t) => n + t.bytes, 0))}</td>
                    <td />
                    {ts.compression && <td />}
                  </tr>
                </tbody>
              </table>
            </div>
          )
        }}
      </Load>
    </Card>
  )
}

function ImportIncident() {
  const imp = useImportIncident()
  const navigate = useNavigate()
  return (
    <Card title="Import an incident" subtitle="Open an incident exported as JSON from this or another LockLens server (up to 10 MiB). It is stored read-only.">
      <label className="block text-sm">
        <span className="mb-1 block text-xs text-muted">Incident JSON file</span>
        <input
          type="file"
          accept="application/json,.json"
          disabled={imp.isPending}
          className="block text-sm"
          onChange={(e) => {
            const f = e.target.files?.[0]
            e.target.value = ''
            if (f) imp.mutate(f, { onSuccess: (r) => navigate(`/incidents/${r.id}`) })
          }}
        />
      </label>
      {imp.error && (
        <div className="mt-3">
          <ErrorBox error={imp.error} title="Import rejected" />
        </div>
      )}
    </Card>
  )
}

interface Section {
  id: string
  label: string
  description: string
  icon: ReactNode
  perm: Permission
  render: () => ReactNode
}

const SECTIONS: Section[] = [
  { id: '', label: 'Instances', description: 'The PostgreSQL servers LockLens monitors.', icon: <Server />, perm: 'targets.manage', render: () => <Targets /> },
  { id: 'users', label: 'Users and roles', description: 'Who can sign in and what each role may do.', icon: <Users />, perm: 'users.manage', render: () => <UsersCard /> },
  {
    id: 'notifications',
    label: 'Notifications',
    description: 'Where incidents are sent, and silences for maintenance.',
    icon: <Bell />,
    perm: 'settings.manage',
    render: () => (
      <div className="space-y-4">
        <NotificationsCard />
        <SilencesCard />
      </div>
    ),
  },
  {
    id: 'rules',
    label: 'Alert rules',
    description: 'When a rule reports a finding or opens an incident.',
    icon: <SlidersHorizontal />,
    perm: 'settings.manage',
    render: () => (
      <Card>
        <div className="pt-5">
          <ThresholdsForm />
        </div>
      </Card>
    ),
  },
  {
    id: 'data',
    label: 'Data and storage',
    description: "LockLens' own database, retention, and incident import.",
    icon: <HardDrive />,
    perm: 'settings.manage',
    render: () => (
      <div className="space-y-4">
        <Storage />
        <ImportIncident />
      </div>
    ),
  },
]

export default function SettingsPage() {
  const can = useCan()
  const id = useParams().section ?? ''
  const sections = SECTIONS.filter((x) => can(x.perm))
  const current = sections.find((x) => x.id === id)
  if (!current) return <Navigate to="/settings" replace />
  return (
    <>
      <PageHeader title="Settings" subtitle="Server settings such as the listen address, retention, and the master key come from environment variables." />
      <div className="grid gap-6 lg:grid-cols-[14rem_minmax(0,1fr)]">
        <nav aria-label="Settings" className="-mx-3 overflow-x-auto px-3 lg:mx-0 lg:overflow-visible lg:px-0">
          <ul className="flex gap-1 lg:sticky lg:top-0 lg:flex-col">
            {sections.map((x) => (
              <li key={x.id}>
                <NavLink
                  to={x.id ? `/settings/${x.id}` : '/settings'}
                  end
                  className={({ isActive }) =>
                    cx(
                      'flex items-center gap-2.5 whitespace-nowrap rounded-lg px-3 py-2 text-sm font-medium [&>svg]:h-4 [&>svg]:w-4',
                      isActive ? 'bg-accent-soft font-semibold text-ink [&>svg]:text-accent' : 'text-ink-2 hover:bg-surface-2 hover:text-ink',
                    )
                  }
                >
                  {x.icon}
                  {x.label}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>
        <section aria-labelledby="settings-section" className="min-w-0">
          <div className="mb-4">
            <h2 id="settings-section" className="text-lg font-semibold tracking-tight text-ink">
              {current.label}
            </h2>
            <p className="mt-0.5 text-sm text-ink-2">{current.description}</p>
          </div>
          {current.render()}
        </section>
      </div>
    </>
  )
}
