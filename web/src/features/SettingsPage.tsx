import { useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { ApiError } from '../api/client'
import { useDeleteTarget, useRuleSettings, useSaveRuleSettings, useSaveTarget, useStorageStats, useTarget, useTargets, useTestConnection, type TestResult } from '../api/hooks'
import type { Target, TargetInput, Thresholds } from '../api/types'
import { Button, Card, ConfirmDialog, DataState, Empty, ErrorBox, Notice, PageHeader, StateBadge, Tag } from '../components/ui'
import { fmtAgo, fmtBytes, fmtNumber } from '../lib/format'

function TargetsList() {
  const q = useTargets()
  const del = useDeleteTarget()
  const [pending, setPending] = useState<{ t: Target; purge: boolean; keep: boolean } | null>(null)
  return (
    <Card title="Targets" actions={<Link to="/settings/targets/new" className="rounded-md border border-accent bg-accent px-3 py-1.5 text-sm font-medium text-accentfg">Add target</Link>}>
      <DataState query={q}>
        {(d) =>
          d.targets.length === 0 ? (
            <Empty title="No targets">Add a PostgreSQL instance to start monitoring.</Empty>
          ) : (
            <ul className="divide-y divide-border">
              {d.targets.map((t) => (
                <li key={t.id} className="flex flex-wrap items-center gap-3 py-2.5">
                  <div className="min-w-0 flex-1">
                    <p className="font-medium">
                      <Link to={`/targets/${t.id}`} className="hover:underline">
                        {t.name}
                      </Link>
                    </p>
                    <p className="text-xs text-muted">
                      {t.host}:{t.port}/{t.database} as {t.user} · every {t.interval_seconds}s · {t.query_text_enabled ? 'query text on' : 'query text off'} · last collected {fmtAgo(t.status.last_success_at)}
                    </p>
                  </div>
                  <StateBadge state={t.status.state} />
                  <Link to={`/settings/targets/${t.id}`} className="rounded-md border border-border px-3 py-1.5 text-sm hover:bg-panel2">
                    Edit
                  </Link>
                  <Button variant="danger" onClick={() => setPending({ t, purge: false, keep: true })}>
                    Delete
                  </Button>
                </li>
              ))}
            </ul>
          )
        }
      </DataState>
      <ConfirmDialog
        open={pending !== null}
        title={`Delete target ${pending?.t.name ?? ''}?`}
        confirmLabel={pending?.purge ? 'Delete target and history' : 'Delete target'}
        danger
        busy={del.isPending}
        onCancel={() => setPending(null)}
        onConfirm={() => pending && del.mutate({ id: pending.t.id, purge: pending.purge, keepIncidents: pending.keep }, { onSuccess: () => setPending(null) })}
      >
        <p>Collection stops immediately. By default the history stays until retention removes it and incidents are kept.</p>
        <label className="mt-3 flex items-center gap-2">
          <input type="checkbox" checked={pending?.purge ?? false} onChange={(e) => pending && setPending({ ...pending, purge: e.target.checked })} />
          Also delete collected history now (cannot be undone)
        </label>
        {pending?.purge && (
          <label className="mt-2 flex items-center gap-2">
            <input type="checkbox" checked={pending.keep} onChange={(e) => setPending({ ...pending, keep: e.target.checked })} />
            Keep incidents and their evidence
          </label>
        )}
        {del.error && <ErrorBox error={del.error} title="Delete failed" />}
      </ConfirmDialog>
    </Card>
  )
}

function TestResultView({ r }: { r: TestResult }) {
  return (
    <div className="space-y-2 text-sm" role="status">
      <p className={r.ok ? 'text-ok' : 'text-crit'}>
        {r.ok ? 'Connection works.' : 'Connection test failed.'} {r.target} · {r.duration_ms} ms
      </p>
      {r.error && <p className="text-crit">{r.error}</p>}
      {r.hint && <p className="text-muted">Hint: {r.hint}</p>}
      {r.capabilities && (
        <p className="text-muted">
          PostgreSQL {r.capabilities.server_version}
          {r.capabilities.is_standby ? ' (standby)' : ''} · pg_stat_statements {r.capabilities.pg_stat_statements ? 'available' : `unavailable: ${r.capabilities.pg_stat_statements_reason ?? ''}`}
        </p>
      )}
      <ul className="space-y-1">
        {r.checks.map((c) => (
          <li key={c.name} className="flex flex-wrap gap-2">
            <Tag tone={c.status === 'ok' ? 'ok' : c.status === 'warn' ? 'warn' : c.status === 'fail' ? 'crit' : 'muted'}>{c.status}</Tag>
            <span className="font-medium">{c.name}</span>
            <span className="text-muted">{c.detail}</span>
            {c.hint && <span className="w-full text-xs text-muted">hint: {c.hint}</span>}
          </li>
        ))}
      </ul>
    </div>
  )
}

const emptyInput: TargetInput = { name: '', description: '', dsn: '', enabled: true, interval_seconds: 10, query_text_enabled: false, max_connections: 2, node_exporter_url: null }

export function TargetFormPage() {
  const params = useParams()
  const id = params.id ? Number(params.id) : undefined
  const existing = useTarget(id ?? 0)
  if (!id) return <TargetForm key="new" initial={emptyInput} />
  return (
    <DataState query={existing}>
      {(t) => (
        <TargetForm
          key={t.id}
          id={t.id}
          existing={t}
          initial={{ name: t.name, description: t.description, dsn: '', enabled: t.enabled, interval_seconds: t.interval_seconds, query_text_enabled: t.query_text_enabled, max_connections: t.max_connections, node_exporter_url: t.node_exporter_url }}
        />
      )}
    </DataState>
  )
}

function TargetForm({ id, existing, initial }: { id?: number; existing?: Target; initial: TargetInput }) {
  const save = useSaveTarget(id)
  const test = useTestConnection()
  const navigate = useNavigate()
  const [input, setInput] = useState<TargetInput>(initial)
  const [confirmText, setConfirmText] = useState(false)
  const fieldErrors = save.error instanceof ApiError ? save.error.fields : {}
  const set = <K extends keyof TargetInput>(k: K, v: TargetInput[K]) => setInput((p) => ({ ...p, [k]: v }))
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (input.query_text_enabled && !(existing?.query_text_enabled ?? false) && !confirmText) {
      setConfirmText(true)
      return
    }
    save.mutate(input, { onSuccess: (t) => navigate(`/targets/${t.id}`) })
  }
  const err = (k: string) => fieldErrors[k] && <p className="mt-1 text-xs text-crit">{fieldErrors[k]}</p>
  return (
    <>
      <PageHeader title={id ? `Edit target ${existing?.name ?? ''}` : 'Add target'} subtitle="LockLens connects read-only. Use a login role with pg_read_all_stats (or pg_monitor); a superuser is not needed." />
      <form onSubmit={submit} className="grid gap-4 lg:grid-cols-2">
        <Card title="Connection">
          <div className="space-y-3 text-sm">
            <label className="block">
              <span className="mb-1 block font-medium">Name</span>
              <input required maxLength={100} className="w-full rounded-md border border-border bg-bg px-2 py-1.5" value={input.name} onChange={(e) => set('name', e.target.value)} />
              {err('name')}
            </label>
            <label className="block">
              <span className="mb-1 block font-medium">Description</span>
              <input maxLength={1000} className="w-full rounded-md border border-border bg-bg px-2 py-1.5" value={input.description} onChange={(e) => set('description', e.target.value)} />
            </label>
            <label className="block">
              <span className="mb-1 block font-medium">Connection string (DSN)</span>
              <input
                type="password"
                autoComplete="off"
                required={!id}
                placeholder={id ? 'Leave empty to keep the stored connection string' : 'postgres://observer@db.example:5432/app?sslmode=verify-full'}
                className="w-full rounded-md border border-border bg-bg px-2 py-1.5 font-mono"
                value={input.dsn}
                onChange={(e) => set('dsn', e.target.value)}
              />
              <span className="mt-1 block text-xs text-muted">Encrypted at rest with the server's master key and never shown again.</span>
              {err('dsn')}
            </label>
            <div className="flex flex-wrap items-center gap-2">
              <Button onClick={() => test.mutate({ id, dsn: input.dsn || undefined })} busy={test.isPending} disabled={!id && !input.dsn}>
                Test connection
              </Button>
            </div>
            {test.error && <ErrorBox error={test.error} title="Test failed" />}
            {test.data && <TestResultView r={test.data} />}
          </div>
        </Card>
        <Card title="Collection">
          <div className="space-y-3 text-sm">
            <label className="flex items-center gap-2">
              <input type="checkbox" checked={input.enabled} onChange={(e) => set('enabled', e.target.checked)} />
              Collection enabled
            </label>
            <label className="block">
              <span className="mb-1 block font-medium">Activity interval (seconds)</span>
              <input type="number" min={5} max={3600} className="w-32 rounded-md border border-border bg-bg px-2 py-1.5" value={input.interval_seconds} onChange={(e) => set('interval_seconds', Number(e.target.value))} />
              <span className="mt-1 block text-xs text-muted">Blocking and sessions use this interval; counters use at least 15 s, statements 60 s, table sizes 5 minutes.</span>
              {err('interval_seconds')}
            </label>
            <label className="block">
              <span className="mb-1 block font-medium">Maximum connections</span>
              <input type="number" min={1} max={5} className="w-32 rounded-md border border-border bg-bg px-2 py-1.5" value={input.max_connections} onChange={(e) => set('max_connections', Number(e.target.value))} />
              {err('max_connections')}
            </label>
            <label className="block">
              <span className="mb-1 block font-medium">node_exporter URL (optional)</span>
              <input
                className="w-full rounded-md border border-border bg-bg px-2 py-1.5 font-mono"
                placeholder="http://db-host:9100/metrics"
                value={input.node_exporter_url ?? ''}
                onChange={(e) => set('node_exporter_url', e.target.value || null)}
              />
              <span className="mt-1 block text-xs text-muted">Host metrics are optional. The exporter must run on the PostgreSQL host; in Docker Desktop it sees the Linux VM, not your computer.</span>
              {err('node_exporter_url')}
            </label>
            <label className="flex items-start gap-2">
              <input type="checkbox" className="mt-1" checked={input.query_text_enabled} onChange={(e) => set('query_text_enabled', e.target.checked)} />
              <span>
                <span className="font-medium">Collect query text</span>
                <span className="block text-xs text-muted">Off by default. Normalised statements and session queries may still contain sensitive literals; no redaction can guarantee their removal. Turning it off deletes stored statement texts.</span>
              </span>
            </label>
          </div>
        </Card>
        <div className="flex gap-2 lg:col-span-2">
          <Button type="submit" variant="primary" busy={save.isPending}>
            {id ? 'Save changes' : 'Add target'}
          </Button>
          <Link to="/settings" className="rounded-md border border-border px-3 py-1.5 text-sm hover:bg-panel2">
            Cancel
          </Link>
          {save.error && !(save.error instanceof ApiError && Object.keys(save.error.fields).length) && <ErrorBox error={save.error} title="Save failed" />}
        </div>
      </form>
      <ConfirmDialog
        open={confirmText}
        title="Enable query text collection?"
        confirmLabel="Enable and save"
        onCancel={() => setConfirmText(false)}
        onConfirm={() => {
          setConfirmText(false)
          save.mutate(input, { onSuccess: (t) => navigate(`/targets/${t.id}`) })
        }}
      >
        <p>SQL text can contain personal data, secrets in literals, or business data. It will be stored in the monitoring store and may appear in exports when explicitly requested.</p>
      </ConfirmDialog>
    </>
  )
}

const THRESHOLD_HELP: Record<string, string> = {
  blocking_min_seconds: 'Blocking must last this long before a finding (seconds).',
  blocking_min_samples: 'Consecutive samples required for blocking findings.',
  connections_warn_ratio: 'Connection usage ratio (0–1) for a warning.',
  long_transaction_warn_seconds: 'Transaction age for a long-transaction warning (seconds); also the long-transaction threshold of views.',
  workload_window_seconds: 'Length of each compared workload window (seconds).',
  regression_min_calls: 'Minimum calls in both windows before a query can be flagged.',
}

function RuleSettings() {
  const q = useRuleSettings()
  const save = useSaveRuleSettings()
  const [draft, setDraft] = useState<Thresholds | null>(null)
  const [confirm, setConfirm] = useState(false)
  return (
    <Card title="Rule thresholds">
      <DataState query={q}>
        {(d) => {
          const values = draft ?? d.thresholds
          return (
            <form
              onSubmit={(e) => {
                e.preventDefault()
                setConfirm(true)
              }}
            >
              <p className="mb-3 text-sm text-muted">Thresholds are validated by the server. Rule versions: {Object.entries(d.rule_versions).length} rules; trigger rules open incidents: {d.trigger_rules.join(', ')}.</p>
              <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
                {Object.keys(d.defaults).map((k) => (
                  <label key={k} className="block text-sm">
                    <span className="block font-mono text-xs">{k}</span>
                    <input
                      type="number"
                      step="any"
                      className="mt-1 w-full rounded-md border border-border bg-bg px-2 py-1"
                      value={values[k] ?? ''}
                      onChange={(e) => setDraft({ ...values, [k]: Number(e.target.value) })}
                    />
                    <span className="block text-xs text-muted">{THRESHOLD_HELP[k] ?? `default ${d.defaults[k]}`}</span>
                  </label>
                ))}
              </div>
              {save.error && (
                <div className="mt-3">
                  <ErrorBox error={save.error} title="Thresholds rejected" />
                </div>
              )}
              {save.isSuccess && !draft && <p className="mt-3 text-sm text-ok">Saved.</p>}
              <div className="mt-3 flex gap-2">
                <Button type="submit" variant="primary" disabled={!draft} busy={save.isPending}>
                  Save thresholds
                </Button>
                <Button onClick={() => setDraft({ ...d.defaults })}>Reset to defaults</Button>
              </div>
              <ConfirmDialog
                open={confirm}
                title="Change rule thresholds?"
                confirmLabel="Save"
                busy={save.isPending}
                onCancel={() => setConfirm(false)}
                onConfirm={() => draft && save.mutate(draft, { onSuccess: () => { setDraft(null); setConfirm(false) }, onError: () => setConfirm(false) })}
              >
                <p>New thresholds apply to the next evaluations of every target. Existing findings and incidents keep the thresholds recorded with them.</p>
              </ConfirmDialog>
            </form>
          )
        }}
      </DataState>
    </Card>
  )
}

function StorageStats() {
  const q = useStorageStats()
  return (
    <Card title="Monitoring store">
      <DataState query={q} loadingLabel="Loading table sizes">
        {(d) => {
          const tables = d.tables ?? []
          const total = tables.reduce((n, t) => n + t.bytes, 0)
          return (
            <>
              <p className="mb-2 text-sm text-muted">
                {fmtBytes(total)} in {tables.length} tables, including indexes. Retention is set with LOCKLENS_RETENTION, LOCKLENS_INCIDENT_RETENTION, and LOCKLENS_FINDING_RETENTION; row counts are planner estimates.
              </p>
              <div className="overflow-x-auto">
                <table className="w-full min-w-[420px] text-sm">
                  <caption className="sr-only">Monitoring store tables</caption>
                  <thead>
                    <tr className="border-b border-border text-left text-xs text-muted">
                      <th className="py-1.5 pr-3 font-medium">Table</th>
                      <th className="py-1.5 pr-3 text-right font-medium">Size</th>
                      <th className="py-1.5 text-right font-medium">Rows (estimate)</th>
                    </tr>
                  </thead>
                  <tbody className="tabular">
                    {tables.map((t) => (
                      <tr key={t.table} className="border-b border-border">
                        <td className="py-1.5 pr-3 font-mono text-xs">{t.table}</td>
                        <td className="py-1.5 pr-3 text-right">{fmtBytes(t.bytes)}</td>
                        <td className="py-1.5 text-right">{fmtNumber(t.estimated_rows)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </>
          )
        }}
      </DataState>
    </Card>
  )
}

export function SettingsPage() {
  return (
    <>
      <PageHeader title="Targets and settings" />
      <div className="space-y-4">
        <Notice tone="info">Credentials are never returned by the API. Server configuration (listen address, retention, master key) comes from environment variables; see docs/configuration.md.</Notice>
        <TargetsList />
        <RuleSettings />
        <StorageStats />
      </div>
    </>
  )
}
