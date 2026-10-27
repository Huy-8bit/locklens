import { useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { Activity, Gauge, Hourglass, Sparkles } from 'lucide-react'
import { usePerformance } from '../api/hooks'
import type { PerfItem } from '../api/types'
import { FindingCard } from '../components/FindingCard'
import { ConfidenceBadge, ImpactBreakdown, ImpactScore } from '../components/InsightParts'
import { PageHeader } from '../components/Shell'
import { SqlText } from '../components/SqlText'
import { tableCls, tdCls, Th, trCls } from '../components/Table'
import { Badge, Card, Drawer, Empty, KpiCard, Load, Notice, selectCls, SeverityBadge, Tabs } from '../components/ui'
import { seconds, time } from '../lib/format'
import { evidence } from '../lib/incident'
import { STATEMENT_RULES, statementRule } from '../lib/insight'
import { useScope } from '../lib/scope'

type Tab = 'anomalies' | 'running'

/** Where to look at a finding's statement or execution. */
function statementLink(it: PerfItem): string | null {
  if (it.statement) return `/queries/${it.target_id}/${it.statement.identity_id}`
  return null
}

function StatementCell({ it }: { it: PerfItem }) {
  const text = it.statement?.query_text
  const qid = it.statement?.key.queryid ?? (evidence(it.finding, 'queryid') as string | undefined)
  const link = statementLink(it)
  const body = text ? (
    <span className="block max-w-[28rem] truncate font-mono text-xs">
      <SqlText sql={text} />
    </span>
  ) : (
    <span className="font-mono text-xs text-ink-2">{qid ? `queryid ${qid}` : 'statement unknown (no query_id)'}</span>
  )
  return link ? (
    <Link to={link} className="block hover:underline" onClick={(e) => e.stopPropagation()}>
      {body}
    </Link>
  ) : (
    body
  )
}

/**
 * Statement performance across the fleet: anomalies against each statement's
 * baseline and statements running for long right now, ranked by impact.
 */
export default function PerformancePage() {
  const { env } = useScope()
  const q = usePerformance(env)
  const [params, setParams] = useSearchParams()
  const tab: Tab = params.get('tab') === 'running' ? 'running' : 'anomalies'
  const rule = params.get('rule') ?? ''
  const [selected, setSelected] = useState<PerfItem | null>(null)
  const set = (k: string, v: string) =>
    setParams(
      (p) => {
        if (v) p.set(k, v)
        else p.delete(k)
        return p
      },
      { replace: true },
    )
  const selectedLink = selected ? statementLink(selected) : null
  const ruleOptions = useMemo(() => Object.entries(STATEMENT_RULES).filter(([id]) => id !== 'queries.long_running'), [])

  return (
    <>
      <PageHeader
        title="Performance"
        subtitle="Statements behaving differently from their own history, and queries running for long right now, ranked by impact. Hypotheses are never confirmed causes."
      />
      <Load q={q} label="Loading performance">
        {(d) => {
          const statements = d.baselines.reduce((n, b) => n + b.statements, 0)
          const warm = d.baselines.reduce((n, b) => n + b.warm, 0)
          const critical = [...d.anomalies, ...d.running].filter((i) => i.finding.severity === 'critical').length
          const rows = (tab === 'running' ? d.running : d.anomalies).filter((i) => !rule || i.finding.rule_id === rule)
          return (
            <div className="space-y-4">
              <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
                <KpiCard icon={<Sparkles />} tone={d.anomalies.length ? 'amber' : 'green'} label="Anomalies" value={String(d.anomalies.length)} sub="statements unlike their baseline" />
                <KpiCard icon={<Hourglass />} tone={d.running.length ? 'amber' : 'green'} label="Running for long" value={String(d.running.length)} sub="still executing" />
                <KpiCard icon={<Gauge />} tone={critical ? 'red' : 'green'} label="Critical" value={String(critical)} sub="open incidents when triggered" />
                <KpiCard icon={<Activity />} tone="violet" label="Baselines" value={`${warm} / ${statements}`} sub={`warm · ${d.baseline_config.days} days, ${seconds(d.baseline_config.point_seconds)} points`} />
              </div>
              {d.unavailable.map((u) => (
                <Notice key={u.target_id} tone="warn">
                  {u.target_name}: pg_stat_statements is not available{u.reason ? ` (${u.reason})` : ''}, so its statements are not analysed.
                </Notice>
              ))}
              <Card pad={false}>
                <div className="flex flex-wrap items-center gap-3 px-5 pt-4 pb-3">
                  <Tabs
                    label="Performance view"
                    value={tab}
                    onChange={(v) => set('tab', v === 'anomalies' ? '' : v)}
                    items={[
                      { value: 'anomalies', label: 'Anomalies', count: d.anomalies.length },
                      { value: 'running', label: 'Running now', count: d.running.length },
                    ]}
                  />
                  {tab === 'anomalies' && (
                    <>
                      <label htmlFor="perf-rule" className="sr-only">
                        Kind of anomaly
                      </label>
                      <select id="perf-rule" className={`${selectCls} ml-auto`} value={rule} onChange={(e) => set('rule', e.target.value)}>
                        <option value="">All kinds</option>
                        {ruleOptions.map(([id, r]) => (
                          <option key={id} value={id}>
                            {r.name}
                          </option>
                        ))}
                      </select>
                    </>
                  )}
                </div>
                {rows.length === 0 ? (
                  <div className="px-5 pb-5">
                    <Empty title={tab === 'running' ? 'No query is running for long' : 'No anomalies'}>
                      {tab === 'running'
                        ? 'Statements executing beyond the slow-query thresholds, or far beyond their usual time, appear here while they run.'
                        : warm === 0 && statements > 0
                          ? 'Baselines are still learning; until then statements are compared with the previous window only.'
                          : 'Every analysed statement is within its usual range.'}
                    </Empty>
                  </div>
                ) : (
                  <div className="overflow-x-auto">
                    <table className={tableCls}>
                      <caption className="sr-only">{tab === 'running' ? 'Queries running for long' : 'Statement anomalies'}, by impact</caption>
                      <thead>
                        <tr>
                          <Th>Impact</Th>
                          <Th>Problem</Th>
                          <Th>Statement</Th>
                          <Th>Instance</Th>
                          <Th>{tab === 'running' ? 'Running for' : 'Since'}</Th>
                        </tr>
                      </thead>
                      <tbody>
                        {rows.map((it) => (
                          <tr key={it.finding_id} className={`${trCls} cursor-pointer`} onClick={() => setSelected(it)}>
                            <td className={tdCls}>
                              <ImpactScore impact={it.impact} />
                            </td>
                            <td className={`${tdCls} max-w-0 min-w-64`}>
                              <button type="button" className="block w-full text-left" onClick={() => setSelected(it)}>
                                <span className="flex flex-wrap items-center gap-1.5">
                                  <SeverityBadge severity={it.finding.severity} />
                                  <Badge tone="accent">{statementRule(it.finding.rule_id).short}</Badge>
                                  <ConfidenceBadge confidence={it.finding.confidence} />
                                </span>
                                <span className="mt-1 block truncate text-sm">{it.finding.title}</span>
                              </button>
                            </td>
                            <td className={`${tdCls} max-w-0 min-w-48`}>
                              <StatementCell it={it} />
                            </td>
                            <td className={tdCls}>
                              <Link to={`/databases/${it.target_id}`} className="text-accent hover:underline" onClick={(e) => e.stopPropagation()}>
                                {it.target_name}
                              </Link>
                            </td>
                            <td className={`${tdCls} num whitespace-nowrap`}>{tab === 'running' ? seconds(it.finding.metrics?.running_seconds ?? null) : time(it.first_seen)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </Card>
              {d.insufficient.length > 0 && (
                <Card title="Not evaluated" subtitle="Rules that could not decide because data is missing. Missing data is never treated as normal.">
                  <ul className="space-y-1 text-sm">
                    {d.insufficient.map((x, i) => (
                      <li key={i}>
                        <strong className="font-medium">{x.target_name}</strong> · {statementRule(x.rule_id).name}: <span className="text-ink-2">{x.reason}</span>
                      </li>
                    ))}
                  </ul>
                </Card>
              )}
              <p className="text-xs text-muted">
                Warm baselines: {d.baselines.map((b) => `${b.target_name} ${b.warm} of ${b.statements} statements`).join(' · ') || 'none yet'} (a baseline needs {d.baseline_config.warmup_points} points). Anomaly at {d.baseline_config.sensitivity} robust deviations. {d.percentiles.reason}
              </p>
            </div>
          )
        }}
      </Load>
      <Drawer open={selected !== null} title={selected?.finding.title ?? ''} onClose={() => setSelected(null)}>
        {selected && (
          <div className="space-y-4">
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <ImpactScore impact={selected.impact} />
              <ConfidenceBadge confidence={selected.finding.confidence} />
              <span className="text-muted">
                {selected.target_name} · first seen {time(selected.first_seen, { date: true })}
              </span>
            </div>
            {selectedLink && (
              <Link to={selectedLink} className="inline-block text-sm text-accent hover:underline">
                Open the statement: baseline, analysis, and plans →
              </Link>
            )}
            {selected.finding.rule_id === 'queries.long_running' && (
              <Link to="/blocking" className="ml-3 inline-block text-sm text-accent hover:underline">
                Open the blocking explorer →
              </Link>
            )}
            <section>
              <h3 className="mb-1 text-sm font-semibold">Why it ranks here</h3>
              <ImpactBreakdown impact={selected.impact} />
            </section>
            {selected.finding.confidence && (
              <section>
                <h3 className="mb-1 text-sm font-semibold">Confidence: {selected.finding.confidence.level}</h3>
                <ul className="list-disc space-y-0.5 pl-5 text-sm text-ink-2">
                  {selected.finding.confidence.reasons.map((r) => (
                    <li key={r}>{r}</li>
                  ))}
                </ul>
              </section>
            )}
            <FindingCard finding={selected.finding} open />
          </div>
        )}
      </Drawer>
    </>
  )
}
