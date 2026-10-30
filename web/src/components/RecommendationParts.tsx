import { useState } from 'react'
import { Link } from 'react-router'
import { useDecideRecommendation, useWhatIf } from '../api/hooks'
import { useCan } from '../auth'
import type { OptimizerTarget, QueryInsightResponse, Recommendation, RecStatement } from '../api/types'
import { bytes, compact, ms, num, pct, time } from '../lib/format'
import { changeLabel, kindInfo, STATUS_INFO } from '../lib/optimizer'
import { ConfidenceBadge } from './InsightParts'
import { CopyButton, SqlText } from './SqlText'
import { Badge, Button, Card, ErrorBox, inputCls, KeyValue, Notice } from './ui'

export function StatusBadge({ status }: { status: Recommendation['status'] }) {
  const s = STATUS_INFO[status]
  return <Badge tone={s.tone}>{s.label}</Badge>
}

function str(v: unknown): string | null {
  return typeof v === 'string' ? v : null
}

function numberOf(v: unknown): number | null {
  return typeof v === 'number' && Number.isFinite(v) ? v : null
}

/** The statements a recommendation is about, with links to them. */
function Statements({ targetId, list }: { targetId: number; list: RecStatement[] }) {
  return (
    <ul className="space-y-2">
      {list.map((s) => (
        <li key={s.identity_id} className="rounded-lg border border-line p-2 text-xs">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-ink-2">
            <Link to={`/queries/${targetId}/${s.identity_id}`} className="font-medium text-accent hover:underline">
              queryid {s.queryid}
            </Link>
            <span>{pct(s.share_of_load, 1)} of execution time</span>
            <span>{compact(s.calls)} calls</span>
            <span>{ms(s.mean_ms)} per call</span>
          </div>
          {s.text && (
            <div className="mt-1 max-h-24 overflow-auto font-mono">
              <SqlText sql={s.text} />
            </div>
          )}
        </li>
      ))}
    </ul>
  )
}

/** The evidence of a recommendation, by kind. */
function Evidence({ rec }: { rec: Recommendation }) {
  const d = rec.details
  const items: [string, string][] = []
  const add = (label: string, v: string | null) => v !== null && items.push([label, v])
  add('Table', str(d.table))
  if (Array.isArray(d.columns)) add('Columns', (d.columns as string[]).join(', '))
  add('Index', str(d.index))
  add('Covered by', str(d.covered_by))
  add('Definition', str(d.definition))
  add('Constraint', str(d.constraint))
  add('References', str(d.references))
  const rows = numberOf(d.rows)
  if (rows !== null) add('Rows (estimate)', compact(rows))
  const size = numberOf(d.size_bytes)
  if (size !== null) add('Size', bytes(size))
  const seq = numberOf(d.seq_scan)
  if (seq !== null) add('Sequential / index scans', `${compact(seq)} / ${compact(numberOf(d.idx_scan))}`)
  const mod = numberOf(d.modified_since_analyze)
  if (mod !== null) add('Rows changed since analyze', compact(mod))
  const est = numberOf(d.estimated_rows)
  if (est !== null) add('Estimated / actual rows per call', `${compact(est)} / ${num(numberOf(d.actual_rows_per_call), 1)}`)
  const spill = numberOf(d.temp_mb_per_call)
  if (spill !== null) add('Spilled per call', `${num(spill, 1)} MB (work_mem ${num((numberOf(d.work_mem_kb) ?? 0) / 1024, 0)} MB)`)
  const share = numberOf(d.share_of_load)
  if (share !== null) add('Share of execution time', pct(share, 1))
  return items.length ? <KeyValue items={items} /> : null
}

/** Everything about one recommendation, with the actions a person can take. */
export function RecommendationDetail({ rec, target }: { rec: Recommendation; target?: OptimizerTarget }) {
  const can = useCan()
  const decide = useDecideRecommendation(rec.target_id)
  const whatIf = useWhatIf(rec.target_id)
  const [note, setNote] = useState(rec.note)
  const info = kindInfo(rec.kind)
  const statements = rec.details.statements ?? (rec.details.statement ? [rec.details.statement] : [])
  const indexRec = rec.kind === 'missing_index' || rec.kind === 'fk_without_index'
  // What-if checks run on the connected database only.
  const otherDatabase = !!rec.database && !!target?.database && rec.database !== target.database
  const hypopgReady = !!target?.hypopg_enabled && !!target.hypopg_installed && !otherDatabase
  const change = changeLabel(rec.before_ms, rec.after_ms)
  const decidable = rec.status === 'open' || rec.status === 'accepted' || rec.status === 'dismissed'

  return (
    <div className="space-y-5 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <StatusBadge status={rec.status} />
        <Badge tone="accent">{info.name}</Badge>
        <ConfidenceBadge confidence={rec.confidence} />
        <span className="text-xs text-muted">benefit {num(rec.benefit, 0)} / 100</span>
      </div>
      <p>{rec.summary}</p>
      <p className="text-xs text-muted">{info.what}</p>
      <section>
        <h3 className="mb-1 text-sm font-semibold">Evidence</h3>
        <Evidence rec={rec} />
        <ul className="mt-2 list-disc space-y-0.5 pl-5 text-xs text-ink-2">
          {rec.confidence.reasons.map((r) => (
            <li key={r}>{r}</li>
          ))}
        </ul>
      </section>
      {statements.length > 0 && (
        <section>
          <h3 className="mb-1 text-sm font-semibold">Statements</h3>
          <Statements targetId={rec.target_id} list={statements} />
        </section>
      )}
      {rec.ddl && (
        <section>
          <div className="mb-1 flex items-center justify-between gap-2">
            <h3 className="text-sm font-semibold">SQL to apply</h3>
            <CopyButton text={rec.ddl} label="Copy SQL" />
          </div>
          <pre className="max-h-48 overflow-auto rounded-lg bg-surface-2 p-3 font-mono text-xs whitespace-pre-wrap">
            <SqlText sql={rec.ddl} />
          </pre>
          <p className="mt-1 text-xs text-muted">LockLens never runs this. Review it, test it on a copy if you can, and run it yourself (CONCURRENTLY avoids blocking writes but cannot run inside a transaction).</p>
        </section>
      )}
      {indexRec && (
        <section>
          <h3 className="mb-1 text-sm font-semibold">What-if check (HypoPG)</h3>
          {rec.whatif ? (
            <div className="space-y-2">
              <p>{rec.whatif.summary}</p>
              <table className="w-full text-xs">
                <caption className="sr-only">Estimated cost per statement without and with the hypothetical index</caption>
                <thead>
                  <tr className="text-left text-muted">
                    <th className="py-1 font-medium">Statement</th>
                    <th className="py-1 text-right font-medium">Cost now</th>
                    <th className="py-1 text-right font-medium">With index</th>
                    <th className="py-1 pl-3 font-medium">Uses it</th>
                  </tr>
                </thead>
                <tbody>
                  {rec.whatif.statements.map((s) => (
                    <tr key={s.identity_id} className="border-t border-line">
                      <td className="py-1 font-mono">{s.queryid}</td>
                      <td className="num py-1 text-right">{s.error ? '—' : num(s.cost_before, 1)}</td>
                      <td className="num py-1 text-right">{s.error ? '—' : num(s.cost_after, 1)}</td>
                      <td className="py-1 pl-3">{s.error ? <span className="text-crit">{s.error}</span> : s.uses_index ? 'yes' : 'no'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <p className="text-xs text-muted">Checked {time(rec.whatif.at, { date: true })}. Costs are the planner’s estimates, not run times.</p>
            </div>
          ) : (
            <p className="text-xs text-ink-2">
              {hypopgReady
                ? 'Plan the statements with this index created hypothetically (in memory only; nothing is written).'
                : otherDatabase
                  ? `What-if checks run on the database of the connection string (${target?.database}); this recommendation is for ${rec.database}.`
                  : !target?.hypopg_installed
                  ? 'The hypopg extension is not installed in this database. Its owner can install it; LockLens never installs extensions.'
                  : 'What-if checks are turned off for this instance (Explorer → access settings).'}
            </p>
          )}
          {can('workspace.sql') && hypopgReady && (
            <Button className="mt-2" size="sm" busy={whatIf.isPending} onClick={() => whatIf.mutate(rec.id)}>
              {rec.whatif ? 'Check again' : 'Check with a hypothetical index'}
            </Button>
          )}
          {whatIf.error && <ErrorBox error={whatIf.error} title="The what-if check failed" />}
        </section>
      )}
      <section>
        <h3 className="mb-1 text-sm font-semibold">History</h3>
        <KeyValue
          items={[
            ['First seen', time(rec.first_seen, { date: true })],
            ['Last derived', time(rec.last_seen, { date: true })],
            ...(rec.decided_at ? ([['Decision', `${rec.status === 'dismissed' ? 'dismissed' : 'accepted'} by ${rec.decided_by ?? '—'} at ${time(rec.decided_at, { date: true })}`]] as [string, string][]) : []),
            ...(rec.applied_at ? ([['Applied', `${time(rec.applied_at, { date: true })}: ${rec.applied_how ?? ''}`]] as [string, string][]) : []),
            ...(rec.before_ms !== null ? ([['Time per call before', ms(rec.before_ms)]] as [string, string][]) : []),
            ...(rec.after_ms !== null ? ([['Time per call after', `${ms(rec.after_ms)}${change ? ` (${change})` : ''}`]] as [string, string][]) : []),
          ]}
        />
        {rec.status === 'applied' && <p className="mt-1 text-xs text-muted">The change was detected in the catalog. Its effect is judged once the statement has run for at least 30 minutes after it.</p>}
        {rec.status === 'regressed' && <Notice tone="crit">The statement got slower after this change. Compare plans on its page and consider reverting.</Notice>}
      </section>
      {decidable && can('incidents.acknowledge') && (
        <section className="space-y-2 border-t border-line pt-3">
          <label className="block text-xs text-muted" htmlFor={`rec-note-${rec.id}`}>
            Note (who will apply it, why it is dismissed, …)
          </label>
          <textarea id={`rec-note-${rec.id}`} className={inputCls} rows={2} maxLength={1000} value={note} onChange={(e) => setNote(e.target.value)} />
          <div className="flex flex-wrap gap-2">
            {rec.status !== 'accepted' && (
              <Button size="sm" variant="primary" busy={decide.isPending} onClick={() => decide.mutate({ id: rec.id, status: 'accepted', note })}>
                Accept
              </Button>
            )}
            {rec.status !== 'dismissed' && (
              <Button size="sm" busy={decide.isPending} onClick={() => decide.mutate({ id: rec.id, status: 'dismissed', note })}>
                Dismiss
              </Button>
            )}
            {rec.status !== 'open' && (
              <Button size="sm" variant="ghost" busy={decide.isPending} onClick={() => decide.mutate({ id: rec.id, status: 'open', note })}>
                Reopen
              </Button>
            )}
          </div>
          {decide.error && <ErrorBox error={decide.error} />}
        </section>
      )}
    </div>
  )
}

/** The statement as PostgreSQL's parser sees it, and the recommendations that name it. */
export function StatementOptimization({ insight }: { insight: QueryInsightResponse }) {
  const [open, setOpen] = useState<number | null>(null)
  const a = insight.analysis
  const recs = insight.recommendations ?? []
  const result = a?.result
  return (
    <Card title="Optimization" subtitle="From PostgreSQL’s own parser and the optimizer. Suggestions, not changes: LockLens never alters the database.">
      <div className="space-y-4 text-sm">
        {!a?.available ? (
          <Notice>This build of LockLens has no SQL parser (built without cgo).</Notice>
        ) : a.error ? (
          <Notice>The statement could not be analysed: {a.error}.</Notice>
        ) : result ? (
          <>
            {result.lints.length > 0 ? (
              <ul className="space-y-1">
                {result.lints.map((l) => (
                  <li key={l.code} className="flex items-start gap-2">
                    <Badge tone={l.severity === 'warning' ? 'warn' : 'muted'}>{l.severity}</Badge>
                    <span>{l.message}</span>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="text-ink-2">No problematic statement shape found.</p>
            )}
            {result.predicates.length > 0 && (
              <div>
                <h4 className="text-xs font-semibold">Conditions an index could serve</h4>
                <ul className="mt-1 flex flex-wrap gap-1.5">
                  {result.predicates.map((p, i) => (
                    <li key={i} className="rounded-md bg-surface-2 px-2 py-0.5 font-mono text-xs">
                      {p.table ? `${p.table}.` : ''}
                      {p.wrapped ? `${p.wrapped}(${p.column})` : p.column} {p.op}
                      {p.in_or ? ' (under OR)' : ''}
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </>
        ) : null}
        {recs.length > 0 && (
          <div>
            <h4 className="text-xs font-semibold">Recommendations for this statement</h4>
            <ul className="mt-1 space-y-2">
              {recs.map((r) => (
                <li key={r.id} className="rounded-lg border border-line">
                  <button type="button" aria-expanded={open === r.id} className="flex w-full flex-wrap items-center gap-2 px-3 py-2 text-left" onClick={() => setOpen(open === r.id ? null : r.id)}>
                    <Badge tone="accent">{kindInfo(r.kind).name}</Badge>
                    <span className="min-w-0 flex-1">{r.title}</span>
                    <StatusBadge status={r.status} />
                  </button>
                  {open === r.id && (
                    <div className="border-t border-line p-3">
                      <RecommendationDetail rec={r} />
                    </div>
                  )}
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>
    </Card>
  )
}
