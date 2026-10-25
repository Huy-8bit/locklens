import { useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { Plus } from 'lucide-react'
import { useDeleteTarget, useImportIncident, useRuleSettings, useSaveRules, useStorage, useTargets } from '../api/hooks'
import type { Target, Thresholds } from '../api/types'
import { PageHeader } from '../components/Shell'
import { UsersCard } from '../components/UsersCard'
import { useCan } from '../auth'
import { tableCls, tdCls, tdNum, Th, trCls } from '../components/Table'
import { Badge, Button, Card, Confirm, Empty, ErrorBox, inputCls, Load, StateBadge } from '../components/ui'
import { bytes, compact, seconds } from '../lib/format'

function Targets() {
  const q = useTargets()
  const del = useDeleteTarget()
  const [pending, setPending] = useState<{ t: Target; purge: boolean } | null>(null)
  return (
    <Card
      pad={false}
      title="Instances"
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
                        <Link className="font-medium hover:underline" to={`/databases/${t.id}`}>
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

function Rules() {
  const q = useRuleSettings()
  const save = useSaveRules()
  const [draft, setDraft] = useState<Thresholds | null>(null)
  return (
    <Card title="Alert thresholds" subtitle="When the rules report a finding or open an incident. Changes apply from the next evaluation.">
      <Load q={q}>
        {(d) => {
          const values = draft ?? d.thresholds
          return (
            <form
              onSubmit={(e) => {
                e.preventDefault()
                if (draft) save.mutate(draft, { onSuccess: () => setDraft(null) })
              }}
            >
              <div className="grid gap-x-4 gap-y-2 sm:grid-cols-2 xl:grid-cols-3">
                {Object.keys(values)
                  .sort()
                  .map((k) => (
                    <label key={k} className="block text-xs">
                      <span className="break-words text-muted">{k.replace(/_/g, ' ')}</span>
                      <input
                        type="number"
                        step="any"
                        className={`${inputCls} mt-0.5`}
                        value={values[k]}
                        onChange={(e) => setDraft({ ...values, [k]: Number(e.target.value) })}
                      />
                      {values[k] !== d.defaults[k] && <span className="text-muted">default {d.defaults[k]}</span>}
                    </label>
                  ))}
              </div>
              {save.error && <div className="mt-3"><ErrorBox error={save.error} title="Thresholds rejected" /></div>}
              <div className="mt-3 flex gap-2">
                <Button type="submit" variant="primary" disabled={!draft} busy={save.isPending}>
                  Save thresholds
                </Button>
                <Button onClick={() => setDraft({ ...d.defaults })}>Reset to defaults</Button>
              </div>
            </form>
          )
        }}
      </Load>
    </Card>
  )
}

function Storage() {
  const q = useStorage()
  return (
    <Card pad={false} title="Monitoring store" subtitle="LockLens' own database. Retention is set with LOCKLENS_RETENTION, LOCKLENS_INCIDENT_RETENTION, and LOCKLENS_FINDING_RETENTION.">
      <Load q={q}>
        {(d) => {
          const tables = d.tables ?? []
          return (
            <div className="overflow-x-auto">
              <table className={tableCls}>
                <caption className="sr-only">Monitoring store tables</caption>
                <thead>
                  <tr>
                    <Th>Table</Th>
                    <Th align="right">Size</Th>
                    <Th align="right">Rows (estimate)</Th>
                  </tr>
                </thead>
                <tbody>
                  {tables.map((t) => (
                    <tr key={t.table} className={trCls}>
                      <td className={`${tdCls} font-mono text-xs`}>{t.table}</td>
                      <td className={tdNum}>{bytes(t.bytes)}</td>
                      <td className={tdNum}>{compact(t.estimated_rows)}</td>
                    </tr>
                  ))}
                  <tr className="border-t border-line-strong font-medium">
                    <td className={tdCls}>Total</td>
                    <td className={tdNum}>{bytes(tables.reduce((n, t) => n + t.bytes, 0))}</td>
                    <td />
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

export default function SettingsPage() {
  const can = useCan()
  return (
    <>
      <PageHeader title="Settings" subtitle="Server settings (listen address, retention, master key) come from environment variables." />
      <div className="space-y-3">
        <Targets />
        {can('users.manage') && <UsersCard />}
        <Rules />
        <ImportIncident />
        <Storage />
      </div>
    </>
  )
}
