import { useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { CircleCheck, Lightbulb, TrendingDown, TriangleAlert } from 'lucide-react'
import { useOptimizer, useRefreshOptimizer } from '../api/hooks'
import { useCan } from '../auth'
import type { OptimizerTarget, Recommendation } from '../api/types'
import { ConfidenceBadge } from '../components/InsightParts'
import { RecommendationDetail, StatusBadge } from '../components/RecommendationParts'
import { PageHeader } from '../components/Shell'
import { tableCls, tdCls, tdNum, Th, trCls } from '../components/Table'
import { Badge, Button, Card, Drawer, Empty, ErrorBox, KpiCard, Load, Notice, Pills, selectCls, SeverityBadge } from '../components/ui'
import { ago, num } from '../lib/format'
import { changeLabel, KIND_INFO, kindInfo, STATUS_FILTERS } from '../lib/optimizer'
import { useScope } from '../lib/scope'

function TargetStatus({ t, parser }: { t: OptimizerTarget; parser: boolean }) {
  const can = useCan()
  const refresh = useRefreshOptimizer(t.target_id)
  const notes: string[] = []
  if (!t.catalog_at) notes.push('the schema catalog has not been collected yet')
  if (!t.query_text_enabled) notes.push('query text is not collected, so statement-based recommendations (indexes, shapes) are off')
  if (!parser) notes.push('this build has no SQL parser, so statement-based recommendations are off')
  return (
    <li className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2 text-sm">
      <Link to={`/databases/${t.target_id}`} className="font-medium text-accent hover:underline">
        {t.target_name}
      </Link>
      <span className="text-xs text-muted">{t.catalog_at ? `catalog of ${t.database} ${ago(t.catalog_at)}` : 'no catalog yet'}</span>
      {t.databases.length > 1 && <span className="text-xs text-muted">{t.databases.length} databases: {t.databases.join(', ')}</span>}
      <Badge tone={t.hypopg_enabled && t.hypopg_installed ? 'good' : 'muted'} title="What-if checks of index recommendations with hypothetical indexes">
        HypoPG {t.hypopg_installed ? (t.hypopg_enabled ? 'on' : 'installed, off') : 'not installed'}
      </Badge>
      {notes.length > 0 && <span className="text-xs text-warn">{notes.join('; ')}</span>}
      {can('incidents.acknowledge') && (
        <Button size="sm" variant="ghost" className="ml-auto" busy={refresh.isPending} onClick={() => refresh.mutate()}>
          Analyse now
        </Button>
      )}
      {refresh.error && <ErrorBox error={refresh.error} />}
    </li>
  )
}

/**
 * Query optimization across the fleet: recommendations from the schema
 * catalog and the workload, ranked by benefit, with their evidence, the SQL
 * to apply, what-if checks, decisions, and the measured effect afterwards.
 */
export default function OptimizePage() {
  const { env } = useScope()
  const [params, setParams] = useSearchParams()
  const status = params.get('status') ?? ''
  const kind = params.get('kind') ?? ''
  const q = useOptimizer(env, status, kind)
  const [selected, setSelected] = useState<number | null>(null)
  const set = (k: string, v: string) =>
    setParams(
      (p) => {
        if (v) p.set(k, v)
        else p.delete(k)
        return p
      },
      { replace: true },
    )
  const kinds = useMemo(() => Object.entries(KIND_INFO), [])

  return (
    <>
      <PageHeader
        title="Optimize"
        subtitle="Recommendations from each database’s schema and workload, ranked by benefit. LockLens never changes a database: review the SQL and apply it yourself; the effect is then measured."
      />
      <Load q={q} label="Loading recommendations">
        {(d) => {
          const c = d.counts
          const open = (c.open ?? 0) + (c.accepted ?? 0)
          const rec: Recommendation | undefined = d.recommendations.find((r) => r.id === selected)
          const target = d.targets.find((t) => t.target_id === rec?.target_id)
          return (
            <div className="space-y-4">
              <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
                <KpiCard icon={<Lightbulb />} tone={open ? 'blue' : 'green'} label="To review" value={String(open)} sub="open and accepted" />
                <KpiCard icon={<TrendingDown />} tone="violet" label="Applied" value={String((c.applied ?? 0) + (c.verified ?? 0) + (c.no_effect ?? 0) + (c.regressed ?? 0))} sub="detected in the catalog" />
                <KpiCard icon={<CircleCheck />} tone="green" label="Improved" value={String(c.verified ?? 0)} sub="statement faster afterwards" />
                <KpiCard icon={<TriangleAlert />} tone={c.regressed ? 'red' : 'green'} label="Slower afterwards" value={String(c.regressed ?? 0)} sub="regressed after a change" />
              </div>
              {!d.parser_available && <Notice tone="warn">This build of LockLens has no SQL parser (built without cgo): only catalog-based recommendations are made.</Notice>}
              <Card title="Instances" subtitle="What the optimizer can see on each instance.">
                <ul className="divide-y divide-line">
                  {d.targets.map((t) => (
                    <TargetStatus key={t.target_id} t={t} parser={d.parser_available} />
                  ))}
                </ul>
              </Card>
              <Card pad={false}>
                <div className="flex flex-wrap items-center gap-3 px-5 pt-4 pb-3">
                  <Pills label="Status" value={status} onChange={(v) => set('status', v)} items={STATUS_FILTERS.map((f) => ({ value: f.value, label: f.label }))} />
                  <label htmlFor="opt-kind" className="sr-only">
                    Kind of recommendation
                  </label>
                  <select id="opt-kind" className={`${selectCls} ml-auto`} value={kind} onChange={(e) => set('kind', e.target.value)}>
                    <option value="">All kinds</option>
                    {kinds.map(([k, v]) => (
                      <option key={k} value={k}>
                        {v.name}
                      </option>
                    ))}
                  </select>
                </div>
                {d.recommendations.length === 0 ? (
                  <div className="px-5 pb-5">
                    <Empty title="No recommendations">{d.targets.some((t) => !t.catalog_at) ? 'Catalogs are collected every 15 minutes; recommendations follow.' : 'Nothing to suggest for the selected filters.'}</Empty>
                  </div>
                ) : (
                  <div className="overflow-x-auto">
                    <table className={tableCls}>
                      <caption className="sr-only">Recommendations, largest benefit first</caption>
                      <thead>
                        <tr>
                          <Th align="right">Benefit</Th>
                          <Th>Recommendation</Th>
                          <Th>Instance</Th>
                          <Th>Status</Th>
                        </tr>
                      </thead>
                      <tbody>
                        {d.recommendations.map((r) => {
                          const change = changeLabel(r.before_ms, r.after_ms)
                          return (
                            <tr key={r.id} className={`${trCls} cursor-pointer`} onClick={() => setSelected(r.id)}>
                              <td className={tdNum}>{num(r.benefit, 0)}</td>
                              <td className={`${tdCls} max-w-0 min-w-72`}>
                                <button type="button" className="block w-full text-left" onClick={() => setSelected(r.id)}>
                                  <span className="flex flex-wrap items-center gap-1.5">
                                    <SeverityBadge severity={r.severity} />
                                    <Badge tone="accent">{kindInfo(r.kind).name}</Badge>
                                    <ConfidenceBadge confidence={r.confidence} />
                                  </span>
                                  <span className="mt-1 block truncate">{r.title}</span>
                                </button>
                              </td>
                              <td className={tdCls}>
                                {r.target_name}
                                {r.database && <span className="block text-xs text-muted">{r.database}</span>}
                              </td>
                              <td className={tdCls}>
                                <StatusBadge status={r.status} />
                                {change && <span className="ml-1 text-xs text-ink-2">{change}</span>}
                              </td>
                            </tr>
                          )
                        })}
                      </tbody>
                    </table>
                  </div>
                )}
              </Card>
              <p className="text-xs text-muted">{d.note}</p>
              <Drawer open={!!rec} title={rec?.title ?? ''} onClose={() => setSelected(null)}>
                {rec && <RecommendationDetail rec={rec} target={target} />}
              </Drawer>
            </div>
          )
        }}
      </Load>
    </>
  )
}
