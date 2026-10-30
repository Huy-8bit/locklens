import { useState, type FormEvent } from 'react'
import { Plus, Trash2 } from 'lucide-react'
import { ApiError } from '../api/client'
import { useChanges, useCreateToken, useDeleteChange, useReportChange, useRevokeToken, useTargets, useTokens } from '../api/hooks'
import type { ApiToken, ChangeEvent, ChangeReport } from '../api/types'
import { useCan } from '../auth'
import { CHANGE_KIND } from '../lib/changes'
import { ago, time } from '../lib/format'
import { useRange } from '../lib/scope'
import { tableCls, tdCls, Th, trCls } from './Table'
import { Badge, Button, Card, Confirm, Drawer, Empty, ErrorBox, Field, inputCls, Load, Notice, selectCls } from './ui'

const detected = (c: ChangeEvent) => c.source === 'detected'

/** One change: what, when, and from where. */
function ChangeRow({ c, onDelete }: { c: ChangeEvent; onDelete?: () => void }) {
  const list = Array.isArray(c.details.changes) ? (c.details.changes as Record<string, unknown>[]) : []
  return (
    <li className="py-2 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <span className="num w-36 shrink-0 text-xs text-muted">{time(c.at, { date: true })}</span>
        <Badge tone={detected(c) ? 'muted' : 'accent'}>{CHANGE_KIND[c.kind] ?? c.kind}</Badge>
        <span className="min-w-0 flex-1 text-ink">{c.title}</span>
        <span className="text-xs text-muted">{detected(c) ? 'detected' : `${c.created_by}${c.target_id === null ? ' · every instance' : ''}`}</span>
        {onDelete && !detected(c) && (
          <Button size="sm" variant="ghost" aria-label="Delete this change" onClick={onDelete}>
            <Trash2 aria-hidden className="h-3.5 w-3.5" />
          </Button>
        )}
      </div>
      {list.length > 1 && (
        <ul className="mt-1 space-y-0.5 pl-6 text-xs text-ink-2">
          {list.slice(0, 8).map((x, i) => (
            <li key={i} className="font-mono">
              {String(x.name ?? '')} {x.change ? String(x.change) : `${String(x.before ?? '')} → ${String(x.after ?? '')}`}
            </li>
          ))}
          {list.length > 8 && <li>and {list.length - 8} more</li>}
        </ul>
      )}
      {detected(c) && typeof c.details.detected_after === 'string' && c.kind !== 'restart' && (
        <p className="mt-0.5 text-xs text-muted">Happened between {time(c.details.detected_after, { date: true })} and {time(String(c.details.detected_by), { date: true })}.</p>
      )}
    </li>
  )
}

function ReportForm({ targetId, onDone }: { targetId: number; onDone: () => void }) {
  const report = useReportChange()
  const [x, setX] = useState<ChangeReport>({ target_id: targetId, kind: 'deploy', title: '' })
  const fields = report.error instanceof ApiError ? report.error.fields : {}
  const submit = (e: FormEvent) => {
    e.preventDefault()
    report.mutate(x, { onSuccess: onDone })
  }
  return (
    <form onSubmit={submit} className="space-y-3">
      <Field label="Kind" error={fields.kind}>
        <select className={selectCls} value={x.kind} onChange={(e) => setX({ ...x, kind: e.target.value as ChangeReport['kind'] })}>
          <option value="deploy">Deploy</option>
          <option value="migration">Migration</option>
          <option value="config">Configuration change</option>
          <option value="note">Note</option>
        </select>
      </Field>
      <Field label="What changed" error={fields.title}>
        <input className={inputCls} required maxLength={200} placeholder="orders-api v2.3.1" value={x.title} onChange={(e) => setX({ ...x, title: e.target.value })} />
      </Field>
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={x.target_id === null} onChange={(e) => setX({ ...x, target_id: e.target.checked ? null : targetId })} />
        It concerns every instance
      </label>
      {report.error && !Object.keys(fields).length && <ErrorBox error={report.error} title="Could not record the change" />}
      <Button type="submit" variant="primary" busy={report.isPending}>
        Record
      </Button>
    </form>
  )
}

/**
 * Changes on an instance in the page's range: detected ones (settings,
 * restarts, schema) and reported ones (deploys). They are also drawn on the
 * charts as dashed lines.
 */
export function ChangesCard({ targetId }: { targetId: number }) {
  const [range] = useRange()
  const q = useChanges([targetId], range)
  const can = useCan()
  const del = useDeleteChange()
  const [adding, setAdding] = useState(false)
  const [deleting, setDeleting] = useState<ChangeEvent | null>(null)
  return (
    <Card
      title="Changes"
      subtitle="Settings, restarts, failovers, and schema changes LockLens detected, and deploys reported by people or pipelines. Also drawn on the charts."
      actions={
        can('incidents.acknowledge') && (
          <Button size="sm" onClick={() => setAdding(true)}>
            <Plus aria-hidden className="h-3.5 w-3.5" /> Record a change
          </Button>
        )
      }
    >
      <Load q={q} label="Loading changes">
        {(d) =>
          d.changes.length === 0 ? (
            <Empty title="No changes in this time range">Settings are compared every minute and the schema every 15 minutes.</Empty>
          ) : (
            <ul className="divide-y divide-line">
              {d.changes.slice(0, 50).map((c) => (
                <ChangeRow key={c.id} c={c} onDelete={can('settings.manage') ? () => setDeleting(c) : undefined} />
              ))}
            </ul>
          )
        }
      </Load>
      <Drawer open={adding} title="Record a change" onClose={() => setAdding(false)}>
        <ReportForm targetId={targetId} onDone={() => setAdding(false)} />
        <p className="mt-4 text-xs text-muted">Pipelines can report deploys with an API token: Settings → API tokens.</p>
      </Drawer>
      <Confirm open={!!deleting} title="Delete this change?" danger confirmLabel="Delete" busy={del.isPending}
        onConfirm={() => deleting && del.mutate(deleting.id, { onSuccess: () => setDeleting(null) })} onCancel={() => setDeleting(null)}>
        {deleting?.title}
      </Confirm>
    </Card>
  )
}

const SCOPE_LABEL: Record<ApiToken['scope'], string> = { events: 'Report changes', ingest: 'Ship logs (agent)' }

function TokenForm({ onCreated }: { onCreated: (secret: string) => void }) {
  const create = useCreateToken()
  const targets = useTargets()
  const [name, setName] = useState('')
  const [scope, setScope] = useState<ApiToken['scope']>('events')
  const [target, setTarget] = useState<number | null>(null)
  const fields = create.error instanceof ApiError ? create.error.fields : {}
  const submit = (e: FormEvent) => {
    e.preventDefault()
    create.mutate({ name, scope, target_id: target }, { onSuccess: (r) => onCreated(r.secret) })
  }
  return (
    <form onSubmit={submit} className="space-y-3">
      <Field label="Name" error={fields.name}>
        <input className={inputCls} required maxLength={100} placeholder="deploy pipeline" value={name} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label="Allows" error={fields.scope}>
        <select className={selectCls} value={scope} onChange={(e) => setScope(e.target.value as ApiToken['scope'])}>
          <option value="events">Reporting changes (POST /api/v1/changes)</option>
          <option value="ingest">Shipping logs from a log agent</option>
        </select>
      </Field>
      <Field label="Instance" hint={scope === 'ingest' ? 'A log agent token belongs to one instance.' : 'Leave empty to allow every instance.'} error={fields.target_id}>
        <select className={selectCls} value={target ?? ''} onChange={(e) => setTarget(e.target.value ? Number(e.target.value) : null)}>
          <option value="">Every instance</option>
          {(targets.data?.targets ?? []).map((t) => (
            <option key={t.id} value={t.id}>
              {t.name}
            </option>
          ))}
        </select>
      </Field>
      {create.error && !Object.keys(fields).length && <ErrorBox error={create.error} title="Could not create the token" />}
      <Button type="submit" variant="primary" busy={create.isPending}>
        Create token
      </Button>
    </form>
  )
}

/** Tokens for machines: deploy pipelines and log agents. */
export function ApiTokensCard() {
  const q = useTokens()
  const targets = useTargets()
  const revoke = useRevokeToken()
  const [adding, setAdding] = useState(false)
  const [secret, setSecret] = useState<string | null>(null)
  const name = (id: number | null) => (id === null ? 'every instance' : (targets.data?.targets.find((t) => t.id === id)?.name ?? `#${id}`))
  return (
    <Card
      title="API tokens"
      subtitle="For machines: a deploy pipeline reports deploys and migrations, a log agent ships PostgreSQL logs. LockLens keeps only a hash of each token."
      actions={
        <Button size="sm" onClick={() => setAdding(true)}>
          <Plus aria-hidden className="h-3.5 w-3.5" /> New token
        </Button>
      }
      pad={false}
    >
      {secret && (
        <div className="p-4">
          <Notice tone="warn">
            Copy this token now; it is not shown again: <code className="select-all break-all font-mono">{secret}</code>
          </Notice>
          <pre className="mt-2 overflow-x-auto rounded-lg bg-surface-2 p-3 text-xs">{`curl -X POST $LOCKLENS_URL/api/v1/changes \\
  -H "Authorization: Bearer $LOCKLENS_TOKEN" -H "Content-Type: application/json" \\
  -d '{"kind":"deploy","title":"orders-api v2.3.1","details":{"commit":"abc123"}}'`}</pre>
        </div>
      )}
      <Load q={q} label="Loading tokens">
        {(d) =>
          d.tokens.length === 0 ? (
            <div className="p-4">
              <Empty title="No tokens">Create one for a deploy pipeline or a log agent.</Empty>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className={tableCls}>
                <thead>
                  <tr>
                    <Th>Name</Th>
                    <Th>Allows</Th>
                    <Th>Instance</Th>
                    <Th>Last used</Th>
                    <Th>{''}</Th>
                  </tr>
                </thead>
                <tbody>
                  {d.tokens.map((t) => (
                    <tr key={t.id} className={trCls}>
                      <td className={tdCls}>
                        {t.name} <span className="font-mono text-xs text-muted">{t.prefix}…</span>
                      </td>
                      <td className={tdCls}>{SCOPE_LABEL[t.scope]}</td>
                      <td className={tdCls}>{name(t.target_id)}</td>
                      <td className={`${tdCls} text-xs text-muted`}>{t.revoked_at ? 'revoked' : t.last_used_at ? ago(t.last_used_at) : 'never'}</td>
                      <td className={tdCls}>
                        {!t.revoked_at && (
                          <Button size="sm" variant="ghost" busy={revoke.isPending && revoke.variables === t.id} onClick={() => revoke.mutate(t.id)}>
                            Revoke
                          </Button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )
        }
      </Load>
      <Drawer open={adding} title="New API token" onClose={() => setAdding(false)}>
        <TokenForm
          onCreated={(s) => {
            setSecret(s)
            setAdding(false)
          }}
        />
      </Drawer>
    </Card>
  )
}
