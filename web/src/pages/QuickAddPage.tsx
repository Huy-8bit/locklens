import { useState, type FormEvent } from 'react'
import { Link, useNavigate } from 'react-router'
import { Eye, EyeOff, ShieldCheck } from 'lucide-react'
import { ApiError } from '../api/client'
import { useInspectConnection, useQuickAdd } from '../api/hooks'
import type { SetupPlan } from '../api/types'
import { PageHeader } from '../components/Shell'
import { Badge, Button, Card, Confirm, ErrorBox, Field, inputCls, KeyValue, Notice } from '../components/ui'
import { useScope } from '../lib/scope'

const ADD_LABEL: Record<string, string> = {
  direct: 'Add instance',
  reuse_roles: 'Add instance',
  create_roles: 'Create roles and add instance',
}

function PlanView({ plan }: { plan: SetupPlan }) {
  const tone = plan.mode === 'unsupported' ? 'crit' : 'info'
  return (
    <div className="space-y-3">
      <KeyValue
        items={[
          ['Server', <span key="s" className="font-mono">{`${plan.host}:${plan.port}/${plan.database}`}</span>],
          ['PostgreSQL', `${plan.server_version} · ${plan.is_standby ? 'standby' : 'primary'}`],
          ['Connected as', <span key="u" className="inline-flex items-center gap-2">{plan.user}{plan.superuser && <Badge tone="warn">superuser</Badge>}</span>],
          ['Query statistics', plan.pg_stat_statements ? 'pg_stat_statements available' : 'pg_stat_statements not installed'],
        ]}
      />
      <Notice tone={tone}>{plan.reason}</Notice>
      {plan.statements.length > 0 && (
        <div>
          <p className="mb-1 text-sm font-medium text-ink">LockLens will run these statements once, then forget the superuser connection:</p>
          <pre tabIndex={0} aria-label="Statements LockLens will run" className="overflow-x-auto rounded-lg bg-surface-2 p-3 font-mono text-xs leading-relaxed text-ink-2">{plan.statements.map((s) => `${s};`).join('\n')}</pre>
          <p className="mt-1 text-xs text-muted">Passwords are generated and stored encrypted; they are never shown.</p>
        </div>
      )}
      {plan.warnings.map((w) => (
        <Notice key={w} tone="warn">
          {w}
        </Notice>
      ))}
    </div>
  )
}

/**
 * Adds an instance from one connection string. LockLens inspects it first
 * and explains what it will do: use it as is, reuse roles it created for
 * the same cluster, or (for a superuser) create least-privilege roles.
 */
export default function QuickAddPage() {
  const navigate = useNavigate()
  const { env } = useScope()
  const [dsn, setDsn] = useState('')
  const [show, setShow] = useState(false)
  const [name, setName] = useState('')
  const [environment, setEnvironment] = useState(env || 'default')
  const [browseData, setBrowseData] = useState(true)
  const [runSql, setRunSql] = useState(true)
  const [confirm, setConfirm] = useState(false)
  const inspect = useInspectConnection()
  const add = useQuickAdd()
  const plan = inspect.data && inspect.variables === dsn ? inspect.data : undefined
  const canAdd = !!plan?.ok && plan.mode !== 'unsupported' && plan.mode !== ''

  const check = (e: FormEvent) => {
    e.preventDefault()
    if (dsn.trim()) inspect.mutate(dsn.trim())
  }
  const submit = (confirmed: boolean) =>
    add.mutate(
      { dsn: dsn.trim(), name: name.trim(), environment, browse_data: browseData && !!plan?.workspace_allowed, run_sql: runSql && !!plan?.workspace_allowed, confirm: confirmed },
      { onSuccess: (r) => navigate(`/databases/${r.target.id}`) },
    )
  const addError = add.error instanceof ApiError ? add.error : null

  return (
    <>
      <PageHeader title="Add an instance" subtitle="Paste a connection string. LockLens checks it and sets up monitoring, the Explorer, and the SQL editor from it." />
      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,22rem)]">
        <Card title="Connection">
          <form onSubmit={check} className="space-y-4">
            <Field label="Connection string" hint="postgres://user:password@host:5432/database?sslmode=… or host=… port=… dbname=… user=… password=…">
              <div className="flex gap-2">
                <input
                  type={show ? 'text' : 'password'}
                  autoComplete="off"
                  spellCheck={false}
                  required
                  className={`${inputCls} font-mono`}
                  placeholder="postgres://postgres:…@db.example:5432/app"
                  value={dsn}
                  onChange={(e) => setDsn(e.target.value)}
                />
                <Button variant="ghost" onClick={() => setShow((s) => !s)} aria-label={show ? 'Hide connection string' : 'Show connection string'}>
                  {show ? <EyeOff aria-hidden className="h-4 w-4" /> : <Eye aria-hidden className="h-4 w-4" />}
                </Button>
              </div>
            </Field>
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="Name (optional)" hint={plan?.suggested_name ? `Default: ${plan.suggested_name}` : 'Defaults to database @ host:port'}>
                <input maxLength={100} className={inputCls} value={name} onChange={(e) => setName(e.target.value)} />
              </Field>
              <Field label="Environment" hint="Groups instances, e.g. production, staging, lab">
                <input className={`${inputCls} font-mono`} list="qa-environments" maxLength={40} value={environment} onChange={(e) => setEnvironment(e.target.value.toLowerCase())} />
                <datalist id="qa-environments">
                  {['production', 'staging', 'development', 'lab', 'default'].map((e) => (
                    <option key={e} value={e} />
                  ))}
                </datalist>
              </Field>
            </div>
            <Button type="submit" busy={inspect.isPending}>
              Check connection
            </Button>
          </form>

          {inspect.error && (
            <div className="mt-4">
              <ErrorBox error={inspect.error} title="Could not check the connection string" />
            </div>
          )}
          {plan && !plan.ok && (
            <div className="mt-4 space-y-2">
              <ErrorBox error={new Error(plan.error ?? 'Cannot connect')} title="Cannot connect" />
              {plan.hint && <p className="text-sm text-ink-2">{plan.hint}</p>}
            </div>
          )}
          {plan?.ok && (
            <div className="mt-5 space-y-4 border-t border-line pt-5">
              <PlanView plan={plan} />
              {canAdd && plan.workspace_allowed && (
                <fieldset className="space-y-2">
                  <legend className="mb-1 text-sm font-medium text-ink">Explorer and SQL editor</legend>
                  <label className="flex items-start gap-2 text-sm">
                    <input type="checkbox" className="mt-1" checked={browseData} onChange={(e) => setBrowseData(e.target.checked)} />
                    <span>
                      Browse table rows
                      <span className="block text-xs text-muted">Operators and administrators can preview rows. Rows can contain personal data.</span>
                    </span>
                  </label>
                  <label className="flex items-start gap-2 text-sm">
                    <input type="checkbox" className="mt-1" checked={runSql} onChange={(e) => setRunSql(e.target.checked)} />
                    <span>
                      Read-only SQL editor
                      <span className="block text-xs text-muted">One statement at a time, in a read-only transaction that is always rolled back.</span>
                    </span>
                  </label>
                  <p className="text-xs text-muted">Table structure is always browsable. You can change these later under Explorer → Access settings.</p>
                </fieldset>
              )}
              {addError && <ErrorBox error={addError} title="The instance was not added" />}
              {canAdd && (
                <Button variant="primary" busy={add.isPending} onClick={() => (plan.mode === 'create_roles' ? setConfirm(true) : submit(false))}>
                  {ADD_LABEL[plan.mode]}
                </Button>
              )}
            </div>
          )}
        </Card>

        <Card title="How it works">
          <ul className="space-y-3 text-sm text-ink-2">
            <li className="flex gap-2">
              <ShieldCheck aria-hidden className="mt-0.5 h-4 w-4 shrink-0 text-good" />
              <span>
                <b className="text-ink">A regular role</b> is used as is, for monitoring and, if it has no administrative privileges, for the Explorer and SQL editor.
              </span>
            </li>
            <li className="flex gap-2">
              <ShieldCheck aria-hidden className="mt-0.5 h-4 w-4 shrink-0 text-good" />
              <span>
                <b className="text-ink">A superuser</b> is never stored. After you confirm, LockLens creates <code>locklens_monitor</code> (pg_monitor) and <code>locklens_reader</code> (pg_read_all_data, read-only) and keeps only those.
              </span>
            </li>
            <li className="flex gap-2">
              <ShieldCheck aria-hidden className="mt-0.5 h-4 w-4 shrink-0 text-good" />
              <span>
                <b className="text-ink">Standbys</b> reuse the roles created on their primary, so add the primary first.
              </span>
            </li>
            <li className="flex gap-2">
              <ShieldCheck aria-hidden className="mt-0.5 h-4 w-4 shrink-0 text-good" />
              <span>LockLens never kills sessions, cancels queries, or changes settings on your servers.</span>
            </li>
          </ul>
          <p className="mt-4 text-xs text-muted">
            Need a custom interval, pool size, or node_exporter? Use the{' '}
            <Link to="/settings/targets/new/manual" className="text-accent underline">
              advanced form
            </Link>
            .
          </p>
        </Card>
      </div>
      <Confirm
        open={confirm}
        title="Create LockLens roles?"
        confirmLabel="Create roles and add"
        busy={add.isPending}
        onCancel={() => setConfirm(false)}
        onConfirm={() => {
          setConfirm(false)
          submit(true)
        }}
      >
        LockLens connects once with the superuser connection string and runs the statements shown, on {plan ? `${plan.host}:${plan.port}` : 'the server'}. The superuser connection string is not stored.
      </Confirm>
    </>
  )
}
