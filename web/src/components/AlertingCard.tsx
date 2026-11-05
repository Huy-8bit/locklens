import { useMemo, useState } from 'react'
import { Plus, Trash2 } from 'lucide-react'
import { useRuleSettings, useSaveAlerting, useTargetPolicy, useTargets } from '../api/hooks'
import type { RuleMode, RuleOverride, RuleSettings } from '../api/types'
import { ruleInfo } from '../lib/incident'
import { fromDisplay, thresholdField, thresholdLabel, toDisplay, UNIT_SUFFIX } from '../lib/thresholds'
import { seconds } from '../lib/format'
import { tableCls, tdCls, Th, trCls } from './Table'
import { Badge, Button, Card, Drawer, Empty, ErrorBox, Field, inputCls, Load, selectCls } from './ui'

const MODE_LABEL: Record<RuleMode, string> = {
  incident: 'Incident + notify',
  finding: 'Finding only',
  off: 'Off',
}
const MODE_TONE: Record<RuleMode, 'crit' | 'accent' | 'muted'> = {
  incident: 'crit',
  finding: 'accent',
  off: 'muted',
}

type Overrides = RuleSettings['overrides']

/** A copy of an object without one key. */
function without<T>(o: Record<string, T> | undefined, key: string): Record<string, T> {
  return Object.fromEntries(Object.entries(o ?? {}).filter(([k]) => k !== key))
}

function ModeSelect({ value, onChange, inherit, label }: { value: RuleMode | ''; onChange: (m: RuleMode | '') => void; inherit?: RuleMode; label: string }) {
  return (
    <select aria-label={label} className={selectCls} value={value} onChange={(e) => onChange(e.target.value as RuleMode | '')}>
      {inherit && <option value="">Inherit ({MODE_LABEL[inherit]})</option>}
      {(['incident', 'finding', 'off'] as RuleMode[]).map((m) => (
        <option key={m} value={m}>
          {MODE_LABEL[m]}
        </option>
      ))}
    </select>
  )
}

function shown(key: string, v: number): string {
  const f = thresholdField(key)
  if (f.unit === 'seconds') return seconds(v)
  const suffix = UNIT_SUFFIX[f.unit]
  return `${toDisplay(f.unit, v).toLocaleString('en-US')}${suffix ? ` ${suffix}` : ''}`
}

/** Edits one override: rule modes and threshold values for an environment or an instance. */
function OverrideForm({
  d,
  initial,
  scope,
  onSave,
  onCancel,
}: {
  d: RuleSettings
  initial: RuleOverride
  scope: string
  onSave: (o: RuleOverride) => void
  onCancel: () => void
}) {
  const [o, setO] = useState<RuleOverride>({
    modes: { ...initial.modes },
    thresholds: { ...initial.thresholds },
  })
  const [key, setKey] = useState('')
  const setMode = (rule: string, m: RuleMode | '') => setO({ ...o, modes: m ? { ...o.modes, [rule]: m } : without(o.modes, rule) })
  const setTh = (k: string, v: number | null) => setO({ ...o, thresholds: v === null ? without(o.thresholds, k) : { ...o.thresholds, [k]: v } })
  return (
    <div className="space-y-5 text-sm">
      <p className="text-ink-2">Values set here replace the global ones for {scope}; everything else is inherited.</p>
      <section>
        <h4 className="mb-2 text-xs font-semibold text-ink">Rules</h4>
        <ul className="space-y-1.5">
          {d.rules.map((r) => (
            <li key={r} className="flex items-center justify-between gap-3">
              <span className="min-w-0">{ruleInfo(r).name}</span>
              <ModeSelect
                label={`Mode of ${ruleInfo(r).name}`}
                value={o.modes?.[r] ?? ''}
                inherit={d.modes[r] ?? d.default_modes[r]}
                onChange={(m) => setMode(r, m)}
              />
            </li>
          ))}
        </ul>
      </section>
      <section>
        <h4 className="mb-2 text-xs font-semibold text-ink">Thresholds</h4>
        <ul className="space-y-2">
          {Object.entries(o.thresholds ?? {}).map(([k, v]) => {
            const f = thresholdField(k)
            return (
              <li key={k} className="flex items-center gap-2">
                <span className="min-w-0 flex-1">{thresholdLabel(k)}</span>
                <input
                  aria-label={thresholdLabel(k)}
                  type="number"
                  step="any"
                  className={`${inputCls} w-28`}
                  value={toDisplay(f.unit, v)}
                  onChange={(e) => setTh(k, fromDisplay(f.unit, Number(e.target.value)))}
                />
                <span className="w-14 text-xs text-muted">{UNIT_SUFFIX[f.unit]}</span>
                <span className="w-32 text-xs text-muted">global {shown(k, d.thresholds[k] ?? 0)}</span>
                <Button size="sm" variant="ghost" aria-label={`Remove ${thresholdLabel(k)}`} onClick={() => setTh(k, null)}>
                  <Trash2 aria-hidden className="h-3.5 w-3.5" />
                </Button>
              </li>
            )
          })}
        </ul>
        <div className="mt-2 flex gap-2">
          <select aria-label="Threshold to override" className={selectCls} value={key} onChange={(e) => setKey(e.target.value)}>
            <option value="">Choose a threshold…</option>
            {d.threshold_keys
              .filter((k) => !(k in (o.thresholds ?? {})))
              .map((k) => (
                <option key={k} value={k}>
                  {thresholdLabel(k)}
                </option>
              ))}
          </select>
          <Button
            size="sm"
            disabled={!key}
            onClick={() => {
              setTh(key, d.thresholds[key] ?? 0)
              setKey('')
            }}
          >
            Add
          </Button>
        </div>
      </section>
      <div className="flex gap-2">
        <Button variant="primary" onClick={() => onSave(o)}>
          Apply
        </Button>
        <Button onClick={onCancel}>Cancel</Button>
      </div>
    </div>
  )
}

/** The thresholds and modes that apply to one instance, and where they come from. */
function EffectivePolicy({ d }: { d: RuleSettings }) {
  const targets = useTargets()
  const list = targets.data?.targets ?? []
  const [id, setId] = useState(0)
  const pick = id || list[0]?.id || 0
  const q = useTargetPolicy(pick)
  return (
    <Card title="What applies to an instance" subtitle="Global values, then its environment's override, then its own.">
      {list.length === 0 ? (
        <Empty title="No instances" />
      ) : (
        <div className="space-y-3 text-sm">
          <select aria-label="Instance" className={selectCls} value={pick} onChange={(e) => setId(Number(e.target.value))}>
            {list.map((t) => (
              <option key={t.id} value={t.id}>
                {t.name} ({t.environment})
              </option>
            ))}
          </select>
          <Load q={q} label="Loading policy">
            {(p) => {
              const changed = Object.keys(p.sources).filter((k) => !k.startsWith('mode:'))
              return (
                <div className="grid gap-4 md:grid-cols-2">
                  <ul className="space-y-1">
                    {d.rules.map((r) => (
                      <li key={r} className="flex items-center justify-between gap-2">
                        <span className="min-w-0 truncate">{ruleInfo(r).name}</span>
                        <span className="flex items-center gap-1">
                          {p.sources[`mode:${r}`] && <Badge>{p.sources[`mode:${r}`]}</Badge>}
                          <Badge tone={MODE_TONE[p.modes[r] ?? 'finding']}>{MODE_LABEL[p.modes[r] ?? 'finding']}</Badge>
                        </span>
                      </li>
                    ))}
                  </ul>
                  <div>
                    <h4 className="text-xs font-semibold text-ink">Thresholds that differ from the global ones</h4>
                    {changed.length === 0 ? (
                      <p className="mt-1 text-xs text-muted">None: every threshold is global.</p>
                    ) : (
                      <ul className="mt-1 space-y-1">
                        {changed.map((k) => (
                          <li key={k} className="flex items-center justify-between gap-2">
                            <span>{thresholdLabel(k)}</span>
                            <span className="flex items-center gap-1">
                              <Badge>{p.sources[k]}</Badge>
                              <span className="num">{shown(k, p.thresholds[k] ?? 0)}</span>
                              <span className="text-xs text-muted">(global {shown(k, d.thresholds[k] ?? 0)})</span>
                            </span>
                          </li>
                        ))}
                      </ul>
                    )}
                  </div>
                </div>
              )
            }}
          </Load>
        </div>
      )}
    </Card>
  )
}

/**
 * Which rules open incidents (and notify), which only report findings, which
 * are off; and overrides of modes and thresholds per environment and per
 * instance.
 */
export function AlertingCard() {
  const q = useRuleSettings()
  const save = useSaveAlerting()
  const targets = useTargets()
  const [editing, setEditing] = useState<{
    kind: 'environment' | 'instance'
    key: string
  } | null>(null)
  const [newKind, setNewKind] = useState<'environment' | 'instance'>('environment')
  const [newKey, setNewKey] = useState('')
  const names = useMemo(() => new Map((targets.data?.targets ?? []).map((t) => [String(t.id), t.name])), [targets.data])
  const envs = useMemo(() => [...new Set((targets.data?.targets ?? []).map((t) => t.environment))].sort(), [targets.data])
  return (
    <Load q={q} label="Loading alerting">
      {(d) => {
        const persist = (modes: RuleSettings['modes'], overrides: Overrides) => save.mutate({ modes, overrides })
        const modeOf = (r: string) => d.modes[r] ?? d.default_modes[r] ?? 'finding'
        const rows: {
          kind: 'environment' | 'instance'
          key: string
          o: RuleOverride
        }[] = [
          ...Object.entries(d.overrides.environments ?? {}).map(([key, o]) => ({
            kind: 'environment' as const,
            key,
            o,
          })),
          ...Object.entries(d.overrides.targets ?? {}).map(([key, o]) => ({
            kind: 'instance' as const,
            key,
            o,
          })),
        ]
        const current = editing && (editing.kind === 'environment' ? d.overrides.environments?.[editing.key] : d.overrides.targets?.[editing.key])
        const apply = (o: RuleOverride) => {
          if (!editing) return
          const next: Overrides = {
            environments: { ...d.overrides.environments },
            targets: { ...d.overrides.targets },
          }
          if (editing.kind === 'environment') next.environments[editing.key] = o
          else next.targets[editing.key] = o
          persist(d.modes, next)
          setEditing(null)
        }
        const remove = (kind: 'environment' | 'instance', key: string) => {
          persist(
            d.modes,
            kind === 'environment'
              ? { environments: without(d.overrides.environments, key), targets: d.overrides.targets }
              : { environments: d.overrides.environments, targets: without(d.overrides.targets, key) },
          )
        }
        return (
          <div className="space-y-4">
            <Card
              pad={false}
              title="What each rule does"
              subtitle="An incident is recorded with its evidence and sent to the notification channels; a finding is shown in LockLens only."
            >
              <div className="overflow-x-auto">
                <table className={tableCls}>
                  <thead>
                    <tr>
                      <Th>Rule</Th>
                      <Th>Everywhere</Th>
                    </tr>
                  </thead>
                  <tbody>
                    {d.rules.map((r) => (
                      <tr key={r} className={trCls}>
                        <td className={tdCls}>
                          <span className="font-medium">{ruleInfo(r).name}</span>
                          <span className="ml-2 font-mono text-xs text-muted">{r}</span>
                        </td>
                        <td className={tdCls}>
                          <ModeSelect
                            label={`Mode of ${ruleInfo(r).name}`}
                            value={modeOf(r)}
                            onChange={(m) => m && persist({ ...d.modes, [r]: m }, d.overrides)}
                          />
                          {modeOf(r) !== d.default_modes[r] && (
                            <span className="ml-2 text-xs text-muted">default: {MODE_LABEL[d.default_modes[r] ?? 'finding']}</span>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </Card>
            <Card
              title="Overrides"
              subtitle="Different modes or thresholds for an environment (for example staging) or one instance. An instance's own override wins over its environment's."
            >
              {rows.length === 0 ? (
                <p className="text-sm text-muted">No overrides: every instance uses the global settings.</p>
              ) : (
                <ul className="divide-y divide-line text-sm">
                  {rows.map((r) => (
                    <li key={`${r.kind}:${r.key}`} className="flex flex-wrap items-center gap-2 py-2">
                      <Badge tone="accent">{r.kind}</Badge>
                      <span className="font-medium">{r.kind === 'instance' ? (names.get(r.key) ?? `#${r.key}`) : r.key}</span>
                      <span className="text-xs text-muted">
                        {Object.keys(r.o.modes ?? {}).length} rule mode(s), {Object.keys(r.o.thresholds ?? {}).length} threshold(s)
                      </span>
                      <span className="ml-auto flex gap-1">
                        <Button size="sm" onClick={() => setEditing({ kind: r.kind, key: r.key })}>
                          Edit
                        </Button>
                        <Button size="sm" variant="ghost" aria-label="Remove override" onClick={() => remove(r.kind, r.key)}>
                          <Trash2 aria-hidden className="h-3.5 w-3.5" />
                        </Button>
                      </span>
                    </li>
                  ))}
                </ul>
              )}
              <div className="mt-3 flex flex-wrap items-end gap-2">
                <Field label="Override for">
                  <select
                    className={selectCls}
                    value={newKind}
                    onChange={(e) => {
                      setNewKind(e.target.value as 'environment' | 'instance')
                      setNewKey('')
                    }}
                  >
                    <option value="environment">an environment</option>
                    <option value="instance">an instance</option>
                  </select>
                </Field>
                <Field label={newKind === 'environment' ? 'Environment' : 'Instance'}>
                  <select className={selectCls} value={newKey} onChange={(e) => setNewKey(e.target.value)}>
                    <option value="">Choose…</option>
                    {newKind === 'environment'
                      ? envs.map((e) => (
                          <option key={e} value={e}>
                            {e}
                          </option>
                        ))
                      : (targets.data?.targets ?? []).map((t) => (
                          <option key={t.id} value={String(t.id)}>
                            {t.name}
                          </option>
                        ))}
                  </select>
                </Field>
                <Button disabled={!newKey} onClick={() => setEditing({ kind: newKind, key: newKey })}>
                  <Plus aria-hidden className="h-3.5 w-3.5" /> Add override
                </Button>
              </div>
              {save.error && (
                <div className="mt-3">
                  <ErrorBox error={save.error} title="Not saved" />
                </div>
              )}
            </Card>
            <EffectivePolicy d={d} />
            <Drawer
              open={!!editing}
              title={editing ? `Override for ${editing.kind === 'instance' ? (names.get(editing.key) ?? editing.key) : `environment ${editing.key}`}` : ''}
              onClose={() => setEditing(null)}
            >
              {editing && (
                <OverrideForm
                  key={`${editing.kind}:${editing.key}`}
                  d={d}
                  initial={current ?? {}}
                  scope={editing.kind === 'instance' ? 'this instance' : 'this environment'}
                  onSave={apply}
                  onCancel={() => setEditing(null)}
                />
              )}
            </Drawer>
          </div>
        )
      }}
    </Load>
  )
}
