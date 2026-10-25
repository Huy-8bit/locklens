import { useState, type FormEvent } from 'react'
import { useNavigate, useParams } from 'react-router'
import { ApiError } from '../api/client'
import { useSaveTarget, useTarget, useTestConnection } from '../api/hooks'
import type { Target, TargetInput, TestResult } from '../api/types'
import { PageHeader } from '../components/Shell'
import { Badge, Button, Card, ErrorBox, Field, inputCls, Load } from '../components/ui'

const empty: TargetInput = { name: '', description: '', dsn: '', enabled: true, interval_seconds: 10, query_text_enabled: true, max_connections: 2, node_exporter_url: null, environment: 'default' }

function TestResultView({ r }: { r: TestResult }) {
  return (
    <div role="status" className="space-y-1 rounded-lg border border-line bg-surface-2 p-3 text-sm">
      <p className={r.ok ? 'font-medium text-good' : 'font-medium text-crit'}>
        {r.ok ? 'Connection works.' : 'Connection test failed.'} <span className="text-xs font-normal text-muted">{r.target} · {r.duration_ms} ms</span>
      </p>
      {r.error && <p className="text-crit">{r.error}</p>}
      {r.hint && <p className="text-muted">Hint: {r.hint}</p>}
      {r.capabilities && (
        <p className="text-ink-2">
          PostgreSQL {r.capabilities.server_version}
          {r.capabilities.is_standby ? ' (standby)' : ''} · query statistics {r.capabilities.pg_stat_statements ? 'available' : `unavailable: ${r.capabilities.pg_stat_statements_reason ?? ''}`} · {r.capabilities.read_all_stats ? 'full visibility' : 'limited visibility (grant pg_read_all_stats)'}
        </p>
      )}
      <ul className="flex flex-wrap gap-1.5 pt-1">
        {r.checks.map((c) => (
          <li key={c.name}>
            <Badge tone={c.status === 'ok' ? 'good' : c.status === 'warn' ? 'warn' : 'crit'} title={c.detail}>
              {c.name}
            </Badge>
          </li>
        ))}
      </ul>
    </div>
  )
}

function TargetForm({ id, initial }: { id?: number; initial: TargetInput }) {
  const navigate = useNavigate()
  const save = useSaveTarget(id)
  const test = useTestConnection()
  const [input, setInput] = useState<TargetInput>(initial)
  const set = <K extends keyof TargetInput>(k: K, v: TargetInput[K]) => setInput((x) => ({ ...x, [k]: v }))
  const fields = save.error instanceof ApiError ? save.error.fields : {}
  const submit = (e: FormEvent) => {
    e.preventDefault()
    save.mutate(input, { onSuccess: (t) => navigate(`/databases/${t.id}`) })
  }
  return (
    <form onSubmit={submit} className="grid gap-3 lg:grid-cols-2">
      <Card title="Connection">
        <div className="space-y-3">
          <Field label="Name" error={fields.name}>
            <input required maxLength={100} className={inputCls} value={input.name} onChange={(e) => set('name', e.target.value)} />
          </Field>
          <Field label="Environment" hint="Groups instances in the environment selector, e.g. production, staging, lab. Lowercase letters, digits, - or _." error={fields.environment}>
            <input className={`${inputCls} font-mono`} list="environments" maxLength={40} value={input.environment} onChange={(e) => set('environment', e.target.value.toLowerCase())} />
            <datalist id="environments">
              {['production', 'staging', 'development', 'lab', 'default'].map((e) => (
                <option key={e} value={e} />
              ))}
            </datalist>
          </Field>
          <Field label="Description">
            <input maxLength={1000} className={inputCls} value={input.description} onChange={(e) => set('description', e.target.value)} />
          </Field>
          <Field label="Connection string (DSN)" hint="Use a login role with pg_read_all_stats. Encrypted at rest and never shown again." error={fields.dsn}>
            <input
              type="password"
              autoComplete="off"
              required={!id}
              className={`${inputCls} font-mono`}
              placeholder={id ? 'Leave empty to keep the stored connection string' : 'postgres://observer@db.example:5432/app?sslmode=verify-full'}
              value={input.dsn}
              onChange={(e) => set('dsn', e.target.value)}
            />
          </Field>
          <Button onClick={() => test.mutate({ id, dsn: input.dsn || undefined })} busy={test.isPending} disabled={!id && !input.dsn}>
            Test connection
          </Button>
          {test.error && <ErrorBox error={test.error} title="Test failed" />}
          {test.data && <TestResultView r={test.data} />}
        </div>
      </Card>
      <Card title="Collection">
        <div className="space-y-3">
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={input.enabled} onChange={(e) => set('enabled', e.target.checked)} />
            Collection enabled
          </label>
          <Field label="Sample interval (seconds)" hint="Sessions and locks use this interval; counters at least 15 s, query statistics 60 s, table sizes 5 minutes." error={fields.interval_seconds}>
            <input type="number" min={5} max={3600} className={`${inputCls} w-32`} value={input.interval_seconds} onChange={(e) => set('interval_seconds', Number(e.target.value))} />
          </Field>
          <Field label="Maximum connections" error={fields.max_connections}>
            <input type="number" min={1} max={5} className={`${inputCls} w-32`} value={input.max_connections} onChange={(e) => set('max_connections', Number(e.target.value))} />
          </Field>
          <Field label="node_exporter URL (optional)" hint="Host CPU, memory, and disk; the exporter must run on the database host." error={fields.node_exporter_url}>
            <input className={`${inputCls} font-mono`} placeholder="http://db-host:9100/metrics" value={input.node_exporter_url ?? ''} onChange={(e) => set('node_exporter_url', e.target.value || null)} />
          </Field>
          <label className="flex items-start gap-2 text-sm">
            <input type="checkbox" className="mt-1" checked={input.query_text_enabled} onChange={(e) => set('query_text_enabled', e.target.checked)} />
            <span>
              <span className="font-medium">Collect SQL text</span>
              <span className="block text-xs text-muted">Shows the SQL of statements and running queries. Normalised statements replace values with $1, $2…, but running queries and some statements can contain literal values. Turning it off deletes stored statement texts.</span>
            </span>
          </label>
        </div>
      </Card>
      <div className="flex items-center gap-2 lg:col-span-2">
        <Button type="submit" variant="primary" busy={save.isPending}>
          {id ? 'Save changes' : 'Add instance'}
        </Button>
        <Button onClick={() => navigate(-1)}>Cancel</Button>
        {save.error && !Object.keys(fields).length && <ErrorBox error={save.error} title="Save failed" />}
      </div>
    </form>
  )
}

function fromTarget(t: Target): TargetInput {
  return { name: t.name, description: t.description, dsn: '', enabled: t.enabled, interval_seconds: t.interval_seconds, query_text_enabled: t.query_text_enabled, max_connections: t.max_connections, node_exporter_url: t.node_exporter_url, environment: t.environment }
}

export default function TargetFormPage() {
  const raw = useParams().targetId
  const id = raw ? Number(raw) : 0
  const existing = useTarget(id)
  return (
    <>
      <PageHeader title={id ? 'Edit instance' : 'Add instance'} subtitle="LockLens connects read-only. A superuser is not needed." />
      {id ? <Load q={existing}>{(t) => <TargetForm key={t.id} id={t.id} initial={fromTarget(t)} />}</Load> : <TargetForm initial={empty} />}
    </>
  )
}
