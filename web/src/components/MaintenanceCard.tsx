import { useState, type FormEvent } from 'react'
import { Plus, Trash2 } from 'lucide-react'
import { ApiError } from '../api/client'
import { useDeleteMaintenance, useMaintenance, useSaveMaintenance, useTargets } from '../api/hooks'
import type { MaintenanceInput, MaintenanceWindow } from '../api/types'
import { RULE_INFO } from '../lib/incident'
import { time } from '../lib/format'
import { Badge, Button, Card, Drawer, Empty, ErrorBox, Field, inputCls, Load, selectCls } from './ui'

const DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

/** When a window applies, in words. */
export function describeWindow(w: Pick<MaintenanceWindow, 'kind' | 'starts_at' | 'ends_at' | 'weekdays' | 'start_time' | 'duration_minutes' | 'timezone'>): string {
  switch (w.kind) {
    case 'once':
      return `${time(w.starts_at, { date: true })} – ${time(w.ends_at, { date: true })}`
    case 'weekly':
      return `${w.weekdays.map((d) => DAYS[d]).join(', ')} at ${w.start_time} (${w.timezone}) for ${w.duration_minutes} min`
    case 'after_deploy':
      return `${w.duration_minutes} min after each reported deploy or migration`
  }
}

/** A datetime-local value of an ISO time, and back. */
const toLocal = (iso: string | null) => (iso ? new Date(new Date(iso).getTime() - new Date(iso).getTimezoneOffset() * 60_000).toISOString().slice(0, 16) : '')
const fromLocal = (v: string) => (v ? new Date(v).toISOString() : null)

function WindowForm({ w, onDone }: { w?: MaintenanceWindow; onDone: () => void }) {
  const save = useSaveMaintenance()
  const targets = useTargets()
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  const [x, setX] = useState<MaintenanceInput>(
    w ?? { name: '', enabled: true, kind: 'weekly', environments: [], target_ids: [], rule_ids: [], starts_at: null, ends_at: null, weekdays: [0], start_time: '02:00', duration_minutes: 120, timezone: tz },
  )
  const [envs, setEnvs] = useState((w?.environments ?? []).join(', '))
  const fields = save.error instanceof ApiError ? save.error.fields : {}
  const submit = (e: FormEvent) => {
    e.preventDefault()
    save.mutate({ id: w?.id, input: { ...x, environments: envs.split(',').map((s) => s.trim()).filter(Boolean) } }, { onSuccess: onDone })
  }
  return (
    <form onSubmit={submit} className="space-y-3">
      <Field label="Name" error={fields.name}>
        <input required maxLength={100} className={inputCls} value={x.name} onChange={(e) => setX({ ...x, name: e.target.value })} placeholder="Sunday patching" />
      </Field>
      <Field label="When" error={fields.kind}>
        <select className={`${selectCls} w-full`} value={x.kind} onChange={(e) => setX({ ...x, kind: e.target.value as MaintenanceInput['kind'] })}>
          <option value="weekly">Every week</option>
          <option value="once">Once</option>
          <option value="after_deploy">After each deploy or migration</option>
        </select>
      </Field>
      {x.kind === 'once' && (
        <div className="grid grid-cols-2 gap-2">
          <Field label="From">
            <input type="datetime-local" required className={inputCls} value={toLocal(x.starts_at)} onChange={(e) => setX({ ...x, starts_at: fromLocal(e.target.value) })} />
          </Field>
          <Field label="Until" error={fields.ends_at}>
            <input type="datetime-local" required className={inputCls} value={toLocal(x.ends_at)} onChange={(e) => setX({ ...x, ends_at: fromLocal(e.target.value) })} />
          </Field>
        </div>
      )}
      {x.kind === 'weekly' && (
        <>
          <fieldset>
            <legend className="mb-1 text-sm font-medium text-ink">Days</legend>
            <div className="flex flex-wrap gap-3 text-sm">
              {DAYS.map((d, i) => (
                <label key={d} className="flex items-center gap-1">
                  <input type="checkbox" checked={x.weekdays.includes(i)}
                    onChange={(e) => setX({ ...x, weekdays: e.target.checked ? [...x.weekdays, i].sort() : x.weekdays.filter((v) => v !== i) })} />
                  {d}
                </label>
              ))}
            </div>
            {fields.weekdays && <p className="text-xs text-crit">{fields.weekdays}</p>}
          </fieldset>
          <div className="grid grid-cols-2 gap-2">
            <Field label="Starts at" error={fields.start_time}>
              <input type="time" required className={inputCls} value={x.start_time} onChange={(e) => setX({ ...x, start_time: e.target.value })} />
            </Field>
            <Field label="Time zone" error={fields.timezone}>
              <input required className={inputCls} value={x.timezone} onChange={(e) => setX({ ...x, timezone: e.target.value })} />
            </Field>
          </div>
        </>
      )}
      {x.kind !== 'once' && (
        <Field label="Duration (minutes)" error={fields.duration_minutes}>
          <input type="number" min={1} className={`${inputCls} w-32`} value={x.duration_minutes} onChange={(e) => setX({ ...x, duration_minutes: Number(e.target.value) })} />
        </Field>
      )}
      <Field label="Environments (optional)" hint="Comma-separated; empty: every environment." error={fields.environments}>
        <input className={inputCls} value={envs} onChange={(e) => setEnvs(e.target.value)} placeholder="staging" />
      </Field>
      <Field label="Only these instances (optional)" error={fields.target_ids}>
        <select multiple size={4} className={`${selectCls} w-full`} value={x.target_ids.map(String)} onChange={(e) => setX({ ...x, target_ids: [...e.target.selectedOptions].map((o) => Number(o.value)) })}>
          {(targets.data?.targets ?? []).map((t) => (
            <option key={t.id} value={t.id}>
              {t.name}
            </option>
          ))}
        </select>
      </Field>
      <Field label="Only these rules (optional)" error={fields.rule_ids}>
        <select multiple size={4} className={`${selectCls} w-full`} value={x.rule_ids} onChange={(e) => setX({ ...x, rule_ids: [...e.target.selectedOptions].map((o) => o.value) })}>
          {Object.entries(RULE_INFO).map(([id, info]) => (
            <option key={id} value={id}>
              {info.name}
            </option>
          ))}
        </select>
      </Field>
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={x.enabled} onChange={(e) => setX({ ...x, enabled: e.target.checked })} /> Enabled
      </label>
      {save.error && !Object.keys(fields).length && <ErrorBox error={save.error} title="Not saved" />}
      <Button type="submit" variant="primary" busy={save.isPending}>
        {w ? 'Save window' : 'Add window'}
      </Button>
    </form>
  )
}

/** Maintenance windows: periods without notifications (incidents are still recorded). */
export function MaintenanceCard() {
  const q = useMaintenance()
  const del = useDeleteMaintenance()
  const [editing, setEditing] = useState<MaintenanceWindow | 'new' | null>(null)
  return (
    <Card
      title="Maintenance windows"
      subtitle="Planned periods without notifications: once, every week, or after each reported deploy. Incidents are still recorded, so the evidence is there afterwards."
      actions={
        <Button size="sm" onClick={() => setEditing('new')}>
          <Plus aria-hidden className="h-4 w-4" /> Add window
        </Button>
      }
    >
      <Load q={q} label="Loading maintenance windows">
        {({ windows }) =>
          windows.length === 0 ? (
            <Empty title="No maintenance windows">For one-off pauses, a silence is quicker.</Empty>
          ) : (
            <ul className="divide-y divide-line text-sm">
              {windows.map((w) => (
                <li key={w.id} className="flex flex-wrap items-center gap-2 py-2">
                  <span className="font-medium">{w.name}</span>
                  {w.enabled ? <Badge tone="accent">{w.kind.replace('_', ' ')}</Badge> : <Badge>disabled</Badge>}
                  <span className="text-xs text-ink-2">{describeWindow(w)}</span>
                  <span className="text-xs text-muted">
                    {[w.environments.length ? w.environments.join(', ') : 'every environment', w.target_ids.length ? `${w.target_ids.length} instance(s)` : '', w.rule_ids.length ? `${w.rule_ids.length} rule(s)` : '']
                      .filter(Boolean)
                      .join(' · ')}
                  </span>
                  <span className="ml-auto flex gap-1">
                    <Button size="sm" onClick={() => setEditing(w)}>Edit</Button>
                    <Button size="sm" variant="ghost" aria-label={`Delete ${w.name}`} busy={del.isPending && del.variables === w.id} onClick={() => del.mutate(w.id)}>
                      <Trash2 aria-hidden className="h-3.5 w-3.5" />
                    </Button>
                  </span>
                </li>
              ))}
            </ul>
          )
        }
      </Load>
      <Drawer open={editing !== null} title={editing === 'new' ? 'New maintenance window' : 'Edit maintenance window'} onClose={() => setEditing(null)}>
        {editing !== null && <WindowForm key={editing === 'new' ? 'new' : editing.id} w={editing === 'new' ? undefined : editing} onDone={() => setEditing(null)} />}
      </Drawer>
    </Card>
  )
}
