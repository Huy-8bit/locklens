import { useState, type FormEvent } from 'react'
import { Link } from 'react-router'
import { BellOff, Plus } from 'lucide-react'
import { ApiError } from '../api/client'
import { useChannels, useCreateSilence, useDeleteChannel, useDeliveries, useEndSilence, useSaveChannel, useSilences, useTargets, useTestChannel } from '../api/hooks'
import type { ChannelDestination, ChannelInput, ChannelKind, Delivery, NotificationChannel, Severity } from '../api/types'
import { useCan } from '../auth'
import { ago, time } from '../lib/format'
import { RULE_INFO } from '../lib/incident'
import { tableCls, tdCls, Th, trCls } from './Table'
import { Badge, Button, Card, Confirm, Drawer, Empty, Field, inputCls, Load, Notice, selectCls } from './ui'

const KIND_LABEL: Record<ChannelKind, string> = { webhook: 'Webhook', slack: 'Slack', email: 'Email' }
const REMINDERS = [
  { value: 0, label: 'Never' },
  { value: 30, label: 'Every 30 minutes' },
  { value: 60, label: 'Every hour' },
  { value: 240, label: 'Every 4 hours' },
  { value: 1440, label: 'Every day' },
]

const statusTone = { sent: 'good', failed: 'crit', silenced: 'muted' } as const

export function DeliveryStatus({ d }: { d: Delivery }) {
  return (
    <Badge tone={statusTone[d.status]} title={d.error ?? undefined}>
      {d.status}
    </Badge>
  )
}

function ChannelForm({ channel, onDone }: { channel?: NotificationChannel; onDone: () => void }) {
  const save = useSaveChannel()
  const [kind, setKind] = useState<ChannelKind>(channel?.kind ?? 'slack')
  const [name, setName] = useState(channel?.name ?? '')
  const [enabled, setEnabled] = useState(channel?.enabled ?? true)
  const [minSeverity, setMinSeverity] = useState<Severity>(channel?.min_severity ?? 'warning')
  const [envs, setEnvs] = useState((channel?.environments ?? []).join(', '))
  const [onOpen, setOnOpen] = useState(channel?.on_open ?? true)
  const [onEscalate, setOnEscalate] = useState(channel?.on_escalate ?? true)
  const [onResolve, setOnResolve] = useState(channel?.on_resolve ?? true)
  const [reminder, setReminder] = useState(Math.round((channel?.reminder_seconds ?? 0) / 60))
  const [replace, setReplace] = useState(!channel)
  const [dest, setDest] = useState<ChannelDestination>({ tls: 'starttls', smtp_port: 587 })
  const [to, setTo] = useState('')
  const fields = save.error instanceof ApiError ? save.error.fields : {}
  const set = (k: keyof ChannelDestination) => (e: { target: { value: string } }) => setDest((d) => ({ ...d, [k]: k === 'smtp_port' ? Number(e.target.value) : e.target.value }))

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const input: ChannelInput = {
      name,
      kind,
      enabled,
      min_severity: minSeverity,
      environments: envs.split(',').map((x) => x.trim()).filter(Boolean),
      on_open: onOpen,
      on_escalate: onEscalate,
      on_resolve: onResolve,
      reminder_minutes: reminder,
    }
    if (replace) input.destination = kind === 'email' ? { ...dest, to: to.split(/[,\s]+/).filter(Boolean) } : { url: dest.url, secret: dest.secret }
    save.mutate({ id: channel?.id, input }, { onSuccess: onDone })
  }

  return (
    <form onSubmit={submit} className="space-y-3">
      <Field label="Name" error={fields.name}>
        <input required maxLength={100} className={inputCls} value={name} onChange={(e) => setName(e.target.value)} placeholder="#db-oncall" />
      </Field>
      {!channel && (
        <Field label="Type" error={fields.kind}>
          <select className={`${selectCls} w-full`} value={kind} onChange={(e) => setKind(e.target.value as ChannelKind)}>
            <option value="slack">Slack (incoming webhook)</option>
            <option value="webhook">Webhook (JSON POST)</option>
            <option value="email">Email (SMTP)</option>
          </select>
        </Field>
      )}
      <fieldset className="space-y-3 rounded-lg border border-line p-3">
        <legend className="px-1 text-xs font-medium text-muted">Destination</legend>
        {channel && (
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={replace} onChange={(e) => setReplace(e.target.checked)} />
            Replace the stored destination ({channel.display || 'hidden'})
          </label>
        )}
        {replace && (kind === 'email' ? (
          <>
            <div className="grid grid-cols-[1fr_6rem] gap-2">
              <Field label="SMTP server" error={fields['destination.smtp_host']}>
                <input required className={inputCls} value={dest.smtp_host ?? ''} onChange={set('smtp_host')} placeholder="smtp.example.com" />
              </Field>
              <Field label="Port" error={fields['destination.smtp_port']}>
                <input type="number" min={1} max={65535} className={inputCls} value={dest.smtp_port ?? 587} onChange={set('smtp_port')} />
              </Field>
            </div>
            <Field label="Security" error={fields['destination.tls']}>
              <select className={`${selectCls} w-full`} value={dest.tls} onChange={set('tls')}>
                <option value="starttls">STARTTLS (port 587)</option>
                <option value="tls">TLS (port 465)</option>
                <option value="none">None (a local relay only; no password)</option>
              </select>
            </Field>
            <div className="grid grid-cols-2 gap-2">
              <Field label="User name (optional)">
                <input autoComplete="off" className={inputCls} value={dest.username ?? ''} onChange={set('username')} />
              </Field>
              <Field label="Password">
                <input type="password" autoComplete="new-password" className={inputCls} value={dest.password ?? ''} onChange={set('password')} />
              </Field>
            </div>
            <Field label="From" error={fields['destination.from']}>
              <input required className={inputCls} value={dest.from ?? ''} onChange={set('from')} placeholder="locklens@example.com" />
            </Field>
            <Field label="To" hint="One or more addresses, separated by commas." error={fields['destination.to']}>
              <input required className={inputCls} value={to} onChange={(e) => setTo(e.target.value)} placeholder="dba@example.com, oncall@example.com" />
            </Field>
          </>
        ) : (
          <>
            <Field label={kind === 'slack' ? 'Incoming webhook URL' : 'URL'} hint="Stored encrypted and never shown again." error={fields['destination.url']}>
              <input required type="url" autoComplete="off" className={inputCls} value={dest.url ?? ''} onChange={set('url')} placeholder={kind === 'slack' ? 'https://hooks.slack.com/services/…' : 'https://example.com/hooks/locklens'} />
            </Field>
            {kind === 'webhook' && (
              <Field label="Signing secret (optional)" hint="Bodies are signed with HMAC-SHA256 in X-LockLens-Signature." error={fields['destination.secret']}>
                <input type="password" autoComplete="new-password" className={inputCls} value={dest.secret ?? ''} onChange={set('secret')} />
              </Field>
            )}
          </>
        ))}
      </fieldset>
      <Field label="Minimum severity" error={fields.min_severity}>
        <select className={`${selectCls} w-full`} value={minSeverity} onChange={(e) => setMinSeverity(e.target.value as Severity)}>
          <option value="info">Info and above</option>
          <option value="warning">Warning and above</option>
          <option value="critical">Critical only</option>
        </select>
      </Field>
      <fieldset className="space-y-1.5">
        <legend className="mb-1 text-sm font-medium text-ink">Send when an incident</legend>
        <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={onOpen} onChange={(e) => setOnOpen(e.target.checked)} /> opens (or opens again)</label>
        <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={onEscalate} onChange={(e) => setOnEscalate(e.target.checked)} /> becomes more severe</label>
        <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={onResolve} onChange={(e) => setOnResolve(e.target.checked)} /> is resolved</label>
        {fields.events && <p className="text-xs text-crit">{fields.events}</p>}
      </fieldset>
      <Field label="Remind while open and unacknowledged" error={fields.reminder_minutes}>
        <select className={`${selectCls} w-full`} value={reminder} onChange={(e) => setReminder(Number(e.target.value))}>
          {REMINDERS.map((r) => (
            <option key={r.value} value={r.value}>
              {r.label}
            </option>
          ))}
        </select>
      </Field>
      <Field label="Environments (optional)" hint="Comma-separated, for example production. Empty sends incidents of every environment." error={fields.environments}>
        <input className={inputCls} value={envs} onChange={(e) => setEnvs(e.target.value)} placeholder="production" />
      </Field>
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} /> Enabled
      </label>
      <Notice>Messages contain the incident title, severity, instance, rule, times, a few observations, and a link. They never contain SQL text or connection details.</Notice>
      {save.error && !Object.keys(fields).length && <Notice tone="crit">{save.error instanceof Error ? save.error.message : 'Could not save'}</Notice>}
      <Button type="submit" variant="primary" busy={save.isPending}>
        {channel ? 'Save channel' : 'Add channel'}
      </Button>
    </form>
  )
}

function eventsOf(c: NotificationChannel): string {
  const e = [c.on_open && 'opened', c.on_escalate && 'escalated', c.on_resolve && 'resolved', c.reminder_seconds > 0 && `reminders every ${Math.round(c.reminder_seconds / 60)} min`].filter(Boolean)
  return e.join(', ')
}

/** Notification channels and their recent deliveries (administrators). */
export function NotificationsCard() {
  const q = useChannels()
  const deliveries = useDeliveries()
  const test = useTestChannel()
  const del = useDeleteChannel()
  const [editing, setEditing] = useState<NotificationChannel | 'new' | null>(null)
  const [deleting, setDeleting] = useState<NotificationChannel | null>(null)
  return (
    <Card
      pad={false}
      title="Channels"
      subtitle="Slack, webhook, and email destinations. Set LOCKLENS_PUBLIC_URL so messages link back to LockLens."
      actions={
        <Button size="sm" onClick={() => setEditing('new')}>
          <Plus aria-hidden className="h-4 w-4" /> Add channel
        </Button>
      }
    >
      <Load q={q} label="Loading channels">
        {({ channels }) =>
          channels.length === 0 ? (
            <div className="px-5 pb-5">
              <Empty title="No notification channels">Add a Slack, webhook, or email channel to hear about incidents without keeping LockLens open.</Empty>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className={tableCls}>
                <caption className="sr-only">Notification channels</caption>
                <thead>
                  <tr>
                    <Th>Name</Th>
                    <Th>Destination</Th>
                    <Th>Sends</Th>
                    <Th>Status</Th>
                    <Th>
                      <span className="sr-only">Actions</span>
                    </Th>
                  </tr>
                </thead>
                <tbody>
                  {channels.map((c) => {
                    const last = deliveries.data?.deliveries.find((d) => d.channel_id === c.id)
                    return (
                      <tr key={c.id} className={trCls}>
                        <td className={tdCls}>
                          <span className="font-medium">{c.name}</span> <Badge>{KIND_LABEL[c.kind]}</Badge>
                        </td>
                        <td className={`${tdCls} font-mono text-xs`}>{c.display}</td>
                        <td className={`${tdCls} text-xs text-ink-2`}>
                          {c.min_severity} and above{c.environments.length ? ` in ${c.environments.join(', ')}` : ''}: {eventsOf(c)}
                        </td>
                        <td className={tdCls}>
                          {c.enabled ? <Badge tone="good">enabled</Badge> : <Badge>disabled</Badge>}
                          {last && (
                            <span className="ml-2 text-xs text-muted">
                              last <DeliveryStatus d={last} /> {ago(last.at)}
                            </span>
                          )}
                        </td>
                        <td className={`${tdCls} whitespace-nowrap text-right`}>
                          <Button size="sm" variant="ghost" busy={test.isPending && test.variables === c.id} onClick={() => test.mutate(c.id)}>
                            Send test
                          </Button>
                          <Button size="sm" variant="ghost" onClick={() => setEditing(c)}>
                            Edit
                          </Button>
                          <Button size="sm" variant="ghost" onClick={() => setDeleting(c)}>
                            Delete
                          </Button>
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
              {test.data && (
                <div className="px-5 py-3">
                  <Notice tone={test.data.status === 'sent' ? 'info' : 'crit'}>
                    Test message {test.data.status === 'sent' ? 'sent' : `failed after ${test.data.attempts} attempt(s): ${test.data.error ?? ''}`}.
                  </Notice>
                </div>
              )}
            </div>
          )
        }
      </Load>
      {(deliveries.data?.deliveries.length ?? 0) > 0 && (
        <details className="border-t border-line px-5 py-3">
          <summary className="cursor-pointer text-sm font-medium text-ink">Recent deliveries</summary>
          <table className={`${tableCls} mt-2`}>
            <caption className="sr-only">Recent notification deliveries</caption>
            <thead>
              <tr>
                <Th>Time</Th>
                <Th>Channel</Th>
                <Th>Event</Th>
                <Th>Incident</Th>
                <Th>Status</Th>
              </tr>
            </thead>
            <tbody>
              {deliveries.data?.deliveries.slice(0, 20).map((d) => (
                <tr key={d.id} className={trCls}>
                  <td className={`${tdCls} num text-xs`}>{time(d.at, { date: true })}</td>
                  <td className={tdCls}>{d.channel_name}</td>
                  <td className={tdCls}>{d.event}</td>
                  <td className={tdCls}>{d.incident_id ? <Link className="text-accent hover:underline" to={`/incidents/${d.incident_id}`}>#{d.incident_id}</Link> : '—'}</td>
                  <td className={tdCls}>
                    <DeliveryStatus d={d} /> {d.error && <span className="text-xs text-muted">{d.error}</span>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </details>
      )}
      <Drawer open={editing !== null} title={editing === 'new' ? 'Add a notification channel' : 'Edit notification channel'} onClose={() => setEditing(null)}>
        {editing !== null && <ChannelForm key={editing === 'new' ? 'new' : editing.id} channel={editing === 'new' ? undefined : editing} onDone={() => setEditing(null)} />}
      </Drawer>
      <Confirm
        open={deleting !== null}
        title={`Delete ${deleting?.name ?? ''}?`}
        confirmLabel="Delete channel"
        danger
        busy={del.isPending}
        onCancel={() => setDeleting(null)}
        onConfirm={() => deleting && del.mutate(deleting.id, { onSuccess: () => setDeleting(null) })}
      >
        Incidents are no longer sent to this channel, and its delivery history is removed.
      </Confirm>
    </Card>
  )
}

const DURATIONS = [
  { value: 60, label: '1 hour' },
  { value: 240, label: '4 hours' },
  { value: 1440, label: '1 day' },
  { value: 10080, label: '7 days' },
]

/** Silences (maintenance windows): notifications stop, incidents are still recorded. */
export function SilencesCard() {
  const can = useCan()
  const q = useSilences()
  const targets = useTargets()
  const create = useCreateSilence()
  const end = useEndSilence()
  const [target, setTarget] = useState('')
  const [rule, setRule] = useState('')
  const [minutes, setMinutes] = useState(60)
  const [reason, setReason] = useState('')
  const fields = create.error instanceof ApiError ? create.error.fields : {}
  return (
    <Card title="Silences" subtitle="Stop notifications for an instance, a rule, or both for a while (maintenance windows). Incidents are still recorded.">
      <Load q={q} label="Loading silences">
        {({ silences }) => (
          <div className="space-y-4">
            {silences.length === 0 ? (
              <p className="text-sm text-muted">No active silence.</p>
            ) : (
              <ul className="space-y-2">
                {silences.map((x) => (
                  <li key={x.id} className="flex flex-wrap items-center gap-2 rounded-lg border border-line px-3 py-2 text-sm">
                    <BellOff aria-hidden className="h-4 w-4 text-muted" />
                    <span className="font-medium">{x.target_name ?? 'Every instance'}</span>
                    <span className="text-ink-2">· {x.rule_id ? RULE_INFO[x.rule_id]?.name ?? x.rule_id : 'every rule'}</span>
                    <span className="text-muted">
                      until {time(x.ends_at, { date: true })} · by {x.created_by}
                      {x.reason ? ` · ${x.reason}` : ''}
                    </span>
                    {can('incidents.acknowledge') && (
                      <Button size="sm" variant="ghost" className="ml-auto" busy={end.isPending && end.variables === x.id} onClick={() => end.mutate(x.id)}>
                        End now
                      </Button>
                    )}
                  </li>
                ))}
              </ul>
            )}
            {can('incidents.acknowledge') && (
              <form
                className="grid gap-2 sm:grid-cols-[1fr_1fr_9rem]"
                onSubmit={(e) => {
                  e.preventDefault()
                  create.mutate({ target_id: target ? Number(target) : null, rule_id: rule || null, minutes, reason }, { onSuccess: () => setReason('') })
                }}
              >
                <Field label="Instance" error={fields.target_id}>
                  <select className={`${selectCls} w-full`} value={target} onChange={(e) => setTarget(e.target.value)}>
                    <option value="">Every instance</option>
                    {(targets.data?.targets ?? []).map((t) => (
                      <option key={t.id} value={t.id}>
                        {t.name}
                      </option>
                    ))}
                  </select>
                </Field>
                <Field label="Rule" error={fields.rule_id}>
                  <select className={`${selectCls} w-full`} value={rule} onChange={(e) => setRule(e.target.value)}>
                    <option value="">Every rule</option>
                    {Object.entries(RULE_INFO).map(([id, r]) => (
                      <option key={id} value={id}>
                        {r.name}
                      </option>
                    ))}
                  </select>
                </Field>
                <Field label="For" error={fields.ends_at}>
                  <select className={`${selectCls} w-full`} value={minutes} onChange={(e) => setMinutes(Number(e.target.value))}>
                    {DURATIONS.map((d) => (
                      <option key={d.value} value={d.value}>
                        {d.label}
                      </option>
                    ))}
                  </select>
                </Field>
                <div className="sm:col-span-3">
                  <Field label="Reason (optional)" error={fields.reason}>
                    <input className={inputCls} maxLength={500} value={reason} onChange={(e) => setReason(e.target.value)} placeholder="Planned failover test" />
                  </Field>
                </div>
                <div>
                  <Button type="submit" busy={create.isPending}>
                    <BellOff aria-hidden className="h-4 w-4" /> Silence
                  </Button>
                </div>
              </form>
            )}
          </div>
        )}
      </Load>
    </Card>
  )
}

/** An incident's notifications, and a quick silence for its instance and rule. */
export function IncidentNotifications({ incidentId, targetId, ruleId }: { incidentId: number; targetId: number | null; ruleId: string }) {
  const can = useCan()
  const deliveries = useDeliveries(incidentId)
  const silences = useSilences()
  const create = useCreateSilence()
  const end = useEndSilence()
  const active = silences.data?.silences.find((x) => (x.target_id === null || x.target_id === targetId) && (x.rule_id === null || x.rule_id === ruleId))
  const list = deliveries.data?.deliveries ?? []
  return (
    <Card title="Notifications">
      <div className="space-y-3 text-sm">
        {list.length === 0 ? (
          <p className="text-muted">No notification was sent for this incident.</p>
        ) : (
          <ul className="space-y-1.5">
            {list.slice(0, 8).map((d) => (
              <li key={d.id} className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
                <DeliveryStatus d={d} />
                <span>
                  {d.event} → {d.channel_name}
                </span>
                <span className="text-xs text-muted">{time(d.at)}</span>
                {d.error && <span className="w-full text-xs text-muted">{d.error}</span>}
              </li>
            ))}
          </ul>
        )}
        {active ? (
          <div className="flex flex-wrap items-center gap-2 rounded-lg bg-surface-2 px-3 py-2">
            <BellOff aria-hidden className="h-4 w-4 text-muted" />
            <span>
              Silenced until {time(active.ends_at, { date: true })}
              {active.reason ? ` (${active.reason})` : ''}
            </span>
            {can('incidents.acknowledge') && (
              <Button size="sm" variant="ghost" className="ml-auto" busy={end.isPending} onClick={() => end.mutate(active.id)}>
                End
              </Button>
            )}
          </div>
        ) : (
          can('incidents.acknowledge') && (
            <div className="flex flex-wrap items-center gap-1.5">
              <span className="text-xs text-muted">Silence this rule on this instance for</span>
              {DURATIONS.slice(0, 3).map((d) => (
                <Button key={d.value} size="sm" busy={create.isPending && create.variables?.minutes === d.value} onClick={() => create.mutate({ target_id: targetId, rule_id: ruleId, minutes: d.value, reason: `from incident #${incidentId}` })}>
                  {d.label}
                </Button>
              ))}
            </div>
          )
        )}
      </div>
    </Card>
  )
}
