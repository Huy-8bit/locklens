import { useState } from 'react'
import { ChevronDown, ChevronRight, CircleHelp, Lightbulb } from 'lucide-react'
import { useIncidentInvestigation } from '../api/hooks'
import type { Confidence, Impact, Investigation } from '../api/types'
import { time } from '../lib/format'
import { CONFIDENCE_TONE, impactTone, statementRule, STRENGTH_TONE } from '../lib/insight'
import { Badge, Card, cx, ErrorBox, Spinner } from './ui'

/** A confidence grade with its reasons on hover and for screen readers. */
export function ConfidenceBadge({ confidence }: { confidence?: Confidence }) {
  if (!confidence) return null
  return (
    <Badge tone={CONFIDENCE_TONE[confidence.level]} title={confidence.reasons.join('; ')}>
      {confidence.level} confidence
      <span className="sr-only">: {confidence.reasons.join('; ')}</span>
    </Badge>
  )
}

const BAR: Record<'crit' | 'warn' | 'good', string> = { crit: 'bg-crit-mark', warn: 'bg-warn-mark', good: 'bg-good-mark' }

/** The impact score as a bar with its number. */
export function ImpactScore({ impact }: { impact: Impact }) {
  const tone = impactTone(impact.score)
  return (
    <span className="inline-flex items-center gap-2" title={impact.components.map((c) => `${c.name}: ${c.points}/${c.max}`).join(' · ')}>
      <span aria-hidden className="relative h-1.5 w-14 overflow-hidden rounded-full bg-surface-2">
        <ImpactFill score={impact.score} cls={BAR[tone]} />
      </span>
      <span className="num w-8 text-right text-sm font-semibold text-ink">{Math.round(impact.score)}</span>
      <span className="sr-only">impact out of 100</span>
    </span>
  )
}

/** The bar width set through the DOM (the Content Security Policy refuses inline style attributes in markup). */
function ImpactFill({ score, cls }: { score: number; cls: string }) {
  return <span ref={(el) => void (el && (el.style.width = `${Math.max(2, Math.min(100, score))}%`))} className={cx('absolute inset-y-0 left-0 rounded-full', cls)} />
}

/** How the impact score was made. */
export function ImpactBreakdown({ impact }: { impact: Impact }) {
  return (
    <div>
      <table className="w-full text-xs">
        <caption className="sr-only">Impact score components</caption>
        <thead>
          <tr className="text-left text-muted">
            <th className="py-1 font-medium">Part</th>
            <th className="py-1 text-right font-medium">Points</th>
            <th className="py-1 pl-3 font-medium">Because</th>
          </tr>
        </thead>
        <tbody>
          {impact.components.map((c) => (
            <tr key={c.name} className="border-t border-line">
              <td className="py-1 pr-2">{c.name}</td>
              <td className="num py-1 text-right font-mono">
                {c.points} / {c.max}
              </td>
              <td className="py-1 pl-3 text-ink-2">{c.detail}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="mt-2 text-xs text-muted">
        Sum × confidence factor {impact.confidence_factor} = <strong className="text-ink">{impact.score}</strong>. The score orders problems by how much they matter now; it is not a probability.
      </p>
    </div>
  )
}

/** Ranked hypotheses with their evidence, the timeline, and what is missing. */
export function InvestigationView({ investigation: inv }: { investigation: Investigation }) {
  const [open, setOpen] = useState<string | null>(inv.hypotheses[0]?.id ?? null)
  return (
    <div className="space-y-4">
      <p className="flex items-start gap-2 text-sm text-ink">
        <Lightbulb aria-hidden className="mt-0.5 h-4 w-4 shrink-0 text-accent" />
        <span>{inv.summary}</span>
      </p>
      {inv.hypotheses.length > 0 && (
        <ol className="space-y-2" aria-label="Hypotheses, most supported first">
          {inv.hypotheses.map((h, i) => {
            const isOpen = open === h.id
            return (
              <li key={h.id} className="rounded-lg border border-line">
                <button type="button" aria-expanded={isOpen} onClick={() => setOpen(isOpen ? null : h.id)} className="flex w-full items-center gap-2 px-3 py-2 text-left">
                  {isOpen ? <ChevronDown aria-hidden className="h-4 w-4 text-muted" /> : <ChevronRight aria-hidden className="h-4 w-4 text-muted" />}
                  <span className="num w-5 text-xs text-muted">{i + 1}.</span>
                  <span className="min-w-0 flex-1 text-sm font-medium">{h.title}</span>
                  <Badge tone={STRENGTH_TONE[h.strength]}>{h.strength} evidence</Badge>
                </button>
                {isOpen && (
                  <div className="space-y-2 border-t border-line px-3 py-2.5 text-sm">
                    <EvidenceList title="Supporting evidence" items={h.supporting} />
                    {h.contradicting.length > 0 && <EvidenceList title="Against it" items={h.contradicting} />}
                    <EvidenceList title="To confirm or rule it out" items={h.next_steps} />
                  </div>
                )}
              </li>
            )
          })}
        </ol>
      )}
      {inv.timeline.length > 0 && (
        <div>
          <h4 className="text-xs font-semibold text-ink">Timeline</h4>
          <ol className="mt-1 space-y-1 text-xs">
            {inv.timeline.map((t, i) => (
              <li key={i} className="flex gap-2">
                <span className="num w-36 shrink-0 text-muted">{time(t.at, { date: true })}</span>
                <Badge tone={t.kind === 'anomaly' ? 'warn' : t.kind === 'gap' ? 'muted' : 'accent'}>{t.kind === 'anomaly' ? statementRule(t.rule_id ?? '').short : t.kind}</Badge>
                <span className="min-w-0 text-ink-2">{t.summary}</span>
              </li>
            ))}
          </ol>
        </div>
      )}
      {inv.missing_evidence.length > 0 && (
        <div>
          <h4 className="flex items-center gap-1 text-xs font-semibold text-ink">
            <CircleHelp aria-hidden className="h-3.5 w-3.5" /> Missing evidence
          </h4>
          <ul className="mt-1 list-disc space-y-0.5 pl-5 text-xs text-ink-2">
            {inv.missing_evidence.map((m) => (
              <li key={m}>{m}</li>
            ))}
          </ul>
        </div>
      )}
      <p className="text-xs text-muted">{inv.disclaimer}</p>
    </div>
  )
}

function EvidenceList({ title, items }: { title: string; items: string[] }) {
  return (
    <div>
      <h5 className="text-xs font-semibold text-ink">{title}</h5>
      <ul className="mt-0.5 list-disc space-y-0.5 pl-5 text-ink-2">
        {items.map((x) => (
          <li key={x}>{x}</li>
        ))}
      </ul>
    </div>
  )
}

/** Correlated evidence of a statement incident as of its trigger. */
export function IncidentRootCause({ incidentId }: { incidentId: number }) {
  const q = useIncidentInvestigation(incidentId, true)
  if (q.error) return <ErrorBox error={q.error} title="Could not correlate the evidence" />
  if (!q.data) return <Spinner label="Correlating evidence" />
  if (!q.data.available || !q.data.investigation) return null
  return (
    <Card title="Possible root causes" subtitle="Evidence collected around the trigger, ranked. Check them in order; none is confirmed.">
      <InvestigationView investigation={q.data.investigation} />
    </Card>
  )
}
