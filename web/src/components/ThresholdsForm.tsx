import { useMemo, useState } from 'react'
import { RotateCcw } from 'lucide-react'
import { useRuleSettings, useSaveRules } from '../api/hooks'
import type { Thresholds } from '../api/types'
import { seconds } from '../lib/format'
import { fromDisplay, THRESHOLD_GROUPS, toDisplay, UNIT_SUFFIX, ungrouped, type ThresholdField } from '../lib/thresholds'
import { Button, cx, ErrorBox, Load } from './ui'

function describeDefault(f: ThresholdField, v: number | undefined): string {
  if (v === undefined) return '—'
  const shown = toDisplay(f.unit, v)
  if (f.unit === 'seconds') return seconds(v)
  return `${shown.toLocaleString('en-US')}${UNIT_SUFFIX[f.unit] ? ` ${UNIT_SUFFIX[f.unit]}` : ''}`
}

function Field({ f, value, def, onChange }: { f: ThresholdField; value: number; def: number | undefined; onChange: (v: number) => void }) {
  const id = `th-${f.key}`
  const changed = def !== undefined && value !== def
  const suffix = UNIT_SUFFIX[f.unit]
  return (
    <div>
      <label htmlFor={id} className="flex items-center gap-1.5 text-sm font-medium text-ink">
        {f.label}
        {changed && (
          <span className="inline-flex items-center gap-1 text-xs font-normal text-accent">
            <span aria-hidden className="h-1.5 w-1.5 rounded-full bg-accent" />
            changed
          </span>
        )}
      </label>
      <div className="mt-1 flex items-stretch overflow-hidden rounded-lg border border-line-strong bg-surface focus-within:outline-2 focus-within:outline-accent">
        <input
          id={id}
          type="number"
          step="any"
          min={0}
          inputMode="decimal"
          className="num w-full min-w-0 bg-transparent px-2.5 py-1.5 text-sm text-ink outline-none"
          value={toDisplay(f.unit, value)}
          onChange={(e) => onChange(fromDisplay(f.unit, Number(e.target.value)))}
          aria-describedby={`${id}-help`}
        />
        {suffix && <span className="flex items-center border-l border-line bg-surface-2 px-2.5 text-xs text-muted">{suffix}</span>}
      </div>
      <p id={`${id}-help`} className="mt-1 flex flex-wrap items-center gap-x-2 text-xs text-muted">
        {f.unit === 'seconds' && value >= 60 && <span className="text-ink-2">= {seconds(value)}</span>}
        {f.help && <span>{f.help}</span>}
        {changed && (
          <button type="button" onClick={() => onChange(def)} className="inline-flex items-center gap-1 text-accent hover:underline">
            <RotateCcw aria-hidden className="h-3 w-3" /> Reset to {describeDefault(f, def)}
          </button>
        )}
      </p>
    </div>
  )
}

/** Rule thresholds, grouped by rule, with units and defaults. */
export function ThresholdsForm() {
  const q = useRuleSettings()
  const save = useSaveRules()
  const [draft, setDraft] = useState<Thresholds | null>(null)
  const saved = q.data?.thresholds
  const changes = useMemo(() => (draft && saved ? Object.keys(draft).filter((k) => draft[k] !== saved[k]).length : 0), [draft, saved])
  return (
    <Load q={q} label="Loading thresholds">
      {(d) => {
        const values = draft ?? d.thresholds
        const set = (k: string, v: number) => setDraft({ ...values, [k]: v })
        const other = ungrouped(Object.keys(values))
        const groups = [
          ...THRESHOLD_GROUPS.map((g) => ({ ...g, fields: g.fields.filter((f) => f.key in values) })).filter((g) => g.fields.length > 0),
          ...(other.length
            ? [{ id: 'other', title: 'Other', description: 'Further thresholds of this server.', fields: other.map((k) => ({ key: k, label: k.replace(/_/g, ' '), unit: 'count' as const })) }]
            : []),
        ]
        return (
          <form
            onSubmit={(e) => {
              e.preventDefault()
              if (draft) save.mutate(draft, { onSuccess: () => setDraft(null) })
            }}
          >
            <div className="divide-y divide-line">
              {groups.map((g) => (
                <section key={g.id} aria-labelledby={`grp-${g.id}`} className="grid gap-4 py-6 first:pt-0 lg:grid-cols-[16rem_minmax(0,1fr)] lg:gap-8">
                  <div>
                    <h3 id={`grp-${g.id}`} className="text-sm font-semibold text-ink">
                      {g.title}
                    </h3>
                    <p className="mt-1 text-sm text-ink-2">{g.description}</p>
                  </div>
                  <div className="grid gap-x-5 gap-y-4 sm:grid-cols-2">
                    {g.fields.map((f) => (
                      <Field key={f.key} f={f} value={values[f.key] ?? 0} def={d.defaults[f.key]} onChange={(v) => set(f.key, v)} />
                    ))}
                  </div>
                </section>
              ))}
            </div>
            {save.error && (
              <div className="mt-3">
                <ErrorBox error={save.error} title="Thresholds rejected" />
              </div>
            )}
            <div className={cx('sticky bottom-0 -mx-5 mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-line bg-surface/95 px-5 py-3 backdrop-blur', changes === 0 && 'border-transparent')}>
              <p className="text-sm text-ink-2" role="status">
                {changes === 0 ? 'Changes apply from the next rule evaluation.' : `${changes} unsaved change${changes === 1 ? '' : 's'}`}
              </p>
              <div className="flex gap-2">
                <Button onClick={() => setDraft({ ...d.defaults })}>Reset all to defaults</Button>
                {changes > 0 && <Button onClick={() => setDraft(null)}>Discard</Button>}
                <Button type="submit" variant="primary" disabled={changes === 0} busy={save.isPending}>
                  Save thresholds
                </Button>
              </div>
            </div>
          </form>
        )
      }}
    </Load>
  )
}
