import { useState } from 'react'
import { ChevronDown, ChevronRight } from 'lucide-react'
import type { Finding } from '../api/types'
import { time } from '../lib/format'
import { Badge, SeverityBadge } from './ui'

function evidenceValue(v: unknown, unit?: string): string {
  if (v === null || v === undefined) return 'unknown'
  if (typeof v === 'number') return `${v.toLocaleString('en-US', { maximumFractionDigits: 2 })}${unit ? ` ${unit}` : ''}`
  if (typeof v === 'object') return JSON.stringify(v)
  return `${String(v)}${unit ? ` ${unit}` : ''}`
}

/** A rule finding: observations with evidence, possible explanations (never a confirmed cause), next checks, limits. */
export function FindingCard({ finding: f, open: initial = false }: { finding: Finding; open?: boolean }) {
  const [open, setOpen] = useState(initial)
  const insufficient = f.status === 'insufficient_data'
  return (
    <div className="rounded-lg border border-line bg-surface">
      <button type="button" onClick={() => setOpen((o) => !o)} aria-expanded={open} className="flex w-full items-start gap-2 px-3 py-2.5 text-left">
        {open ? <ChevronDown aria-hidden className="mt-0.5 h-4 w-4 shrink-0 text-muted" /> : <ChevronRight aria-hidden className="mt-0.5 h-4 w-4 shrink-0 text-muted" />}
        <span className="min-w-0 flex-1">
          <span className="flex flex-wrap items-center gap-2">
            {insufficient ? <Badge>not evaluated</Badge> : <SeverityBadge severity={f.severity} />}
            <span className="text-sm font-medium">{f.title}</span>
          </span>
          <span className="mt-0.5 block text-xs text-muted">
            {f.rule_id} · {time(f.observed_from, { date: true })} – {time(f.observed_to)}
          </span>
        </span>
      </button>
      {open && (
        <div className="space-y-3 border-t border-line px-3 py-3 text-sm">
          {f.observed.length > 0 && (
            <ul className="list-disc space-y-0.5 pl-5">
              {f.observed.map((o) => (
                <li key={o}>{o}</li>
              ))}
            </ul>
          )}
          {f.explanation && <p className="text-ink-2">{f.explanation}</p>}
          {f.evidence.length > 0 && (
            <table className="w-full text-xs">
              <caption className="sr-only">Evidence</caption>
              <thead>
                <tr className="text-left text-muted">
                  <th className="py-1 font-medium">Evidence</th>
                  <th className="py-1 font-medium">Value</th>
                  <th className="py-1 font-medium">Source</th>
                </tr>
              </thead>
              <tbody>
                {f.evidence.map((e, i) => (
                  <tr key={i} className="border-t border-line">
                    <td className="py-1 pr-2">{e.label}</td>
                    <td className="num py-1 pr-2 font-mono">{evidenceValue(e.value, e.unit)}</td>
                    <td className="py-1 font-mono text-muted">{e.source}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {f.possible_explanations.length > 0 && (
            <div>
              <h4 className="text-xs font-semibold text-ink">Possible explanations (not confirmed)</h4>
              <ul className="mt-1 list-disc space-y-0.5 pl-5 text-ink-2">
                {f.possible_explanations.map((x) => (
                  <li key={x}>{x}</li>
                ))}
              </ul>
            </div>
          )}
          {f.next_checks.length > 0 && (
            <div>
              <h4 className="text-xs font-semibold text-ink">What to check next</h4>
              <ul className="mt-1 list-disc space-y-0.5 pl-5 text-ink-2">
                {f.next_checks.map((x) => (
                  <li key={x}>{x}</li>
                ))}
              </ul>
            </div>
          )}
          {f.limitations && <p className="text-xs text-muted">Limitations: {f.limitations}</p>}
        </div>
      )}
    </div>
  )
}
