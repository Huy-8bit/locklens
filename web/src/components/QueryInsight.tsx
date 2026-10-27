import { useState } from 'react'
import { Link } from 'react-router'
import { useCapturePlan, useDeletePlan, usePlan, usePlanCompare, usePlans, useSavePlan } from '../api/hooks'
import { useCan } from '../auth'
import type { PlanNode, QueryInsightResponse } from '../api/types'
import { ms, num, time } from '../lib/format'
import { statementRule, warmupLabel } from '../lib/insight'
import { FindingCard } from './FindingCard'
import { ConfidenceBadge, ImpactBreakdown, ImpactScore, InvestigationView } from './InsightParts'
import { Badge, Button, Card, Confirm, Empty, ErrorBox, inputCls, KeyValue, Notice, selectCls, SeverityBadge, Spinner } from './ui'

/** The statement compared with its own history: baseline, anomalies, impact, and correlated evidence. */
export function PerformanceAnalysis({ insight: d }: { insight: QueryInsightResponse }) {
  const b = d.baseline
  const usual = (m: string) => b.current.stats[m]
  const mean = usual('mean_ms')
  const median = (m: string, digits: number) => {
    const st = usual(m)
    return st ? num(st.median, digits) : '—'
  }
  return (
    <Card
      title="Performance analysis"
      subtitle={b.warm ? `Compared with its baseline (${b.current_basis}, ${b.days} day(s) of history).` : 'The baseline is still learning; until it is ready, rules compare with the previous window only.'}
    >
      <div className="space-y-4">
        <div className="grid gap-4 md:grid-cols-2">
          <KeyValue
            items={[
              ['Baseline', warmupLabel(b.points, b.warmup_points)],
              ['Usual time per call', mean ? `${ms(mean.median)} (± ${ms(mean.mad * 1.4826)})` : 'not enough calls yet'],
              ['Usual calls per second', median('calls_per_s', 2)],
              ['Usual rows per call', median('rows_per_call', 1)],
              ['Usual blocks read per call', median('reads_per_call', 1)],
            ]}
          />
          <div className="text-xs text-muted">
            <p>
              The usual range is the median ± {b.sensitivity} robust deviations (MAD × 1.4826) of {b.point_seconds >= 3600 ? 'hourly' : `${Math.round(b.point_seconds / 60)}-minute`} points from {time(b.from, { date: true })} to {time(b.to, { date: true })}. Periods without collection are left out, never counted as zero; points with a statistics reset are skipped.
            </p>
            <p className="mt-2">{d.percentiles.reason}</p>
          </div>
        </div>
        {d.anomalies.length === 0 ? (
          <Notice>No current anomaly for this statement.</Notice>
        ) : (
          <div className="space-y-3">
            {d.anomalies.map((a) => (
              <div key={a.finding_id} className="rounded-lg border border-line p-3">
                <div className="flex flex-wrap items-center gap-2">
                  <ImpactScore impact={a.impact} />
                  <SeverityBadge severity={a.finding.severity} />
                  <Badge tone="accent">{statementRule(a.finding.rule_id).name}</Badge>
                  <ConfidenceBadge confidence={a.finding.confidence} />
                  <span className="text-xs text-muted">since {time(a.first_seen, { date: true })}</span>
                </div>
                <p className="mt-2 text-sm">{a.finding.observed[0]}</p>
                <details className="mt-2">
                  <summary className="cursor-pointer text-xs text-accent">Impact score and evidence</summary>
                  <div className="mt-2 space-y-3">
                    <ImpactBreakdown impact={a.impact} />
                    <FindingCard finding={a.finding} />
                  </div>
                </details>
              </div>
            ))}
          </div>
        )}
        {d.investigation && (
          <section>
            <h3 className="mb-2 text-sm font-semibold">Likely explanations</h3>
            <InvestigationView investigation={d.investigation} />
          </section>
        )}
        {d.history.length > 0 && (
          <details>
            <summary className="cursor-pointer text-xs text-accent">Earlier anomalies ({d.history.length})</summary>
            <ul className="mt-2 space-y-1 text-sm">
              {d.history.slice(0, 20).map((h) => (
                <li key={h.finding_id} className="flex flex-wrap items-center gap-2">
                  <span className="num text-xs text-muted">
                    {time(h.first_seen, { date: true })} – {time(h.last_seen)}
                  </span>
                  <Badge>{statementRule(h.finding.rule_id).short}</Badge>
                  <span className="text-ink-2">{h.finding.title}</span>
                </li>
              ))}
            </ul>
          </details>
        )}
      </div>
    </Card>
  )
}

function PlanTree({ nodes }: { nodes: PlanNode[] }) {
  return (
    <ol className="space-y-0.5 font-mono text-xs">
      {nodes.map((n, i) => (
        <li key={i} className="flex gap-2">
          <span aria-hidden className="shrink-0 text-muted">
            {'  '.repeat(n.depth)}
            {n.depth > 0 ? '→' : ''}
          </span>
          <span className="min-w-0">
            <strong className="font-semibold">{n.node_type}</strong>
            {n.index && <> using {n.index}</>}
            {n.relation && <> on {n.relation}</>}
            <span className="text-muted">
              {' '}
              cost {num(n.total_cost, 1)} · rows {num(n.plan_rows)}
              {n.actual_total_time !== undefined && ` · actual ${ms(n.actual_total_time)}`}
            </span>
            {(n.filter || n.cond) && <span className="block text-ink-2">{n.cond ? `cond: ${n.cond}` : `filter: ${n.filter}`}</span>}
          </span>
        </li>
      ))}
    </ol>
  )
}

/** Saved execution plans of the statement: capture, paste, view, compare. */
export function PlansCard({ targetId, qid, serverMajor, hasText }: { targetId: number; qid: number; serverMajor: number; hasText: boolean }) {
  const can = useCan()
  const plans = usePlans(targetId, qid)
  const save = useSavePlan(targetId, qid)
  const capture = useCapturePlan(targetId, qid)
  const del = useDeletePlan(targetId)
  const [viewing, setViewing] = useState<number | null>(null)
  const [cmpA, setCmpA] = useState<number | null>(null)
  const [cmpB, setCmpB] = useState<number | null>(null)
  const [paste, setPaste] = useState('')
  const [note, setNote] = useState('')
  const [deleting, setDeleting] = useState<number | null>(null)
  const plan = usePlan(targetId, viewing)
  const cmp = usePlanCompare(targetId, cmpA, cmpB)
  const list = plans.data?.plans ?? []
  const canSave = can('incidents.acknowledge')

  return (
    <Card title="Execution plans" subtitle="Saved on request only. LockLens never runs EXPLAIN ANALYZE; EXPLAIN runs through the audited, read-only SQL workspace.">
      <div className="space-y-4">
        {plans.error && <ErrorBox error={plans.error} />}
        {plans.isLoading && <Spinner label="Loading plans" />}
        {list.length === 0 && !plans.isLoading ? (
          <Empty title="No saved plan">Capture the generic plan (PostgreSQL 16+) or paste EXPLAIN output to compare plans over time.</Empty>
        ) : (
          <ul className="divide-y divide-line rounded-lg border border-line text-sm">
            {list.map((p) => (
              <li key={p.id} className="flex flex-wrap items-center gap-2 px-3 py-2">
                <span className="num text-xs text-muted">{time(p.captured_at, { date: true })}</span>
                <Badge>{p.source.replace('_', ' ')}</Badge>
                <span className="font-mono text-xs" title="Plan shape fingerprint">
                  {p.fingerprint}
                </span>
                {p.total_cost !== null && <span className="text-xs text-ink-2">cost {num(p.total_cost, 1)}</span>}
                <span className="min-w-0 flex-1 truncate text-xs text-ink-2">
                  {p.captured_by}
                  {p.note ? ` · ${p.note}` : ''}
                </span>
                <Button size="sm" variant="ghost" onClick={() => setViewing(viewing === p.id ? null : p.id)}>
                  {viewing === p.id ? 'Hide' : 'View'}
                </Button>
                {canSave && (
                  <Button size="sm" variant="ghost" onClick={() => setDeleting(p.id)}>
                    Delete
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}
        {viewing !== null && (
          <div className="rounded-lg bg-surface-2 p-3">
            {plan.error && <ErrorBox error={plan.error} />}
            {plan.data && (
              <>
                <PlanTree nodes={plan.data.parsed.nodes} />
                {plan.data.index_hints.length > 0 && (
                  <div className="mt-3">
                    <h4 className="text-xs font-semibold">Index candidates (hypotheses)</h4>
                    <ul className="mt-1 list-disc space-y-0.5 pl-5 text-xs text-ink-2">
                      {plan.data.index_hints.map((h) => (
                        <li key={h.relation + h.columns.join()}>
                          <span className="font-mono">
                            {h.relation} ({h.columns.join(', ')})
                          </span>
                          : {h.reason}
                        </li>
                      ))}
                    </ul>
                    <p className="mt-1 text-xs text-muted">{plan.data.index_hints_note}</p>
                  </div>
                )}
              </>
            )}
          </div>
        )}
        {list.length >= 2 && (
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <span>Compare</span>
            <PlanSelect label="Older plan" value={cmpA} onChange={setCmpA} plans={list} />
            <span>with</span>
            <PlanSelect label="Newer plan" value={cmpB} onChange={setCmpB} plans={list} />
          </div>
        )}
        {cmp.data && (
          <div className="rounded-lg border border-line p-3 text-sm">
            <p>
              {cmp.data.comparison.same_shape ? 'Same plan shape.' : 'The plan shape changed.'}
              {cmp.data.comparison.cost_ratio !== null && ` Estimated cost ratio ${num(cmp.data.comparison.cost_ratio, 2)}×.`}
            </p>
            {cmp.data.comparison.changes.length > 0 && (
              <ul className="mt-1 list-disc space-y-0.5 pl-5 text-ink-2">
                {cmp.data.comparison.changes.map((c, i) => (
                  <li key={i}>{c.detail}</li>
                ))}
              </ul>
            )}
          </div>
        )}
        {canSave && (
          <div className="space-y-2 border-t border-line pt-3">
            <div className="flex flex-wrap items-center gap-2">
              {can('workspace.sql') && (
                <Button
                  size="sm"
                  busy={capture.isPending}
                  disabled={serverMajor < 16 || !hasText}
                  title={serverMajor < 16 ? 'Generic plans need PostgreSQL 16 or later' : !hasText ? 'Query text collection is off for this instance' : undefined}
                  onClick={() => capture.mutate(note)}
                >
                  Capture generic plan
                </Button>
              )}
              {can('workspace.sql') && (
                <Link to={`/sql/${targetId}`} className="text-xs text-accent hover:underline">
                  Run EXPLAIN in the SQL editor
                </Link>
              )}
            </div>
            {capture.error && <ErrorBox error={capture.error} title="Could not capture the plan" />}
            <label className="block text-xs text-muted" htmlFor="plan-paste">
              Or paste EXPLAIN output (JSON or text)
            </label>
            <textarea id="plan-paste" className={`${inputCls} font-mono text-xs`} rows={4} value={paste} onChange={(e) => setPaste(e.target.value)} />
            <div className="flex flex-wrap items-center gap-2">
              <input aria-label="Note" placeholder="Note (optional)" className={`${inputCls} max-w-sm`} value={note} maxLength={500} onChange={(e) => setNote(e.target.value)} />
              <Button size="sm" disabled={!paste.trim()} busy={save.isPending} onClick={() => save.mutate({ plan: paste, note }, { onSuccess: () => setPaste('') })}>
                Save plan
              </Button>
            </div>
            {save.error && <ErrorBox error={save.error} title="Could not save the plan" />}
          </div>
        )}
      </div>
      <Confirm
        open={deleting !== null}
        title="Delete this plan?"
        confirmLabel="Delete"
        danger
        busy={del.isPending}
        onCancel={() => setDeleting(null)}
        onConfirm={() =>
          deleting !== null &&
          del.mutate(deleting, {
            onSuccess: () => {
              if (viewing === deleting) setViewing(null)
              setDeleting(null)
            },
          })
        }
      >
        The saved plan is removed from LockLens. Nothing changes on the database.
      </Confirm>
    </Card>
  )
}

function PlanSelect({ label, value, onChange, plans }: { label: string; value: number | null; onChange: (v: number | null) => void; plans: { id: number; captured_at: string; fingerprint: string }[] }) {
  return (
    <select aria-label={label} className={selectCls} value={value ?? ''} onChange={(e) => onChange(e.target.value ? Number(e.target.value) : null)}>
      <option value="">Choose…</option>
      {plans.map((p) => (
        <option key={p.id} value={p.id}>
          {time(p.captured_at, { date: true })} · {p.fingerprint.slice(0, 8)}
        </option>
      ))}
    </select>
  )
}
