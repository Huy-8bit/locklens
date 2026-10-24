import { Link, useNavigate, useSearchParams } from 'react-router'
import { useImportIncident, useIncidents, useTargets } from '../api/hooks'
import { Card, DataState, Empty, ErrorBox, PageHeader, Pagination, SeverityBadge, Tag } from '../components/ui'
import { fmtDuration, fmtTime } from '../lib/format'

export function IncidentsPage() {
  const [params, setParams] = useSearchParams()
  const state = params.get('state') ?? ''
  const target = params.get('target') ?? ''
  const rule = params.get('rule') ?? ''
  const page = Math.max(1, Number(params.get('page') ?? '1') || 1)
  const q = useIncidents({ state, target, rule, page })
  const targets = useTargets()
  const imp = useImportIncident()
  const navigate = useNavigate()
  const set = (k: string, v: string) =>
    setParams(
      (p) => {
        const n = new URLSearchParams(p)
        if (v) n.set(k, v)
        else n.delete(k)
        if (k !== 'page') n.delete('page')
        return n
      },
      { replace: true },
    )
  return (
    <>
      <PageHeader
        title="Incidents"
        subtitle="Recorded automatically when a trigger rule fires. Evidence is stored as observed; acknowledging only adds a note."
        actions={
          <label className="text-sm">
            <span className="text-muted">Import an exported incident (JSON)</span>
            <input
              type="file"
              accept="application/json,.json"
              disabled={imp.isPending}
              className="mt-1 block text-xs"
              onChange={(e) => {
                const f = e.target.files?.[0]
                e.target.value = ''
                if (f) imp.mutate(f, { onSuccess: (r) => navigate(`/incidents/${r.id}`) })
              }}
            />
          </label>
        }
      />
      {imp.error && (
        <div className="mb-4">
          <ErrorBox error={imp.error} title="Import rejected" />
        </div>
      )}
      <Card>
        <div className="mb-3 flex flex-wrap gap-3 text-sm">
          <label className="flex items-center gap-2">
            <span className="text-muted">State</span>
            <select className="rounded-md border border-border bg-panel px-2 py-1" value={state} onChange={(e) => set('state', e.target.value)}>
              <option value="">All</option>
              <option value="open">Open</option>
              <option value="resolved">Resolved</option>
            </select>
          </label>
          <label className="flex items-center gap-2">
            <span className="text-muted">Target</span>
            <select className="rounded-md border border-border bg-panel px-2 py-1" value={target} onChange={(e) => set('target', e.target.value)}>
              <option value="">All</option>
              {targets.data?.targets.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name}
                </option>
              ))}
            </select>
          </label>
          <label className="flex items-center gap-2">
            <span className="text-muted">Rule</span>
            <select className="rounded-md border border-border bg-panel px-2 py-1" value={rule} onChange={(e) => set('rule', e.target.value)}>
              <option value="">All</option>
              {q.data?.rules.map((r) => (
                <option key={r} value={r}>
                  {r}
                </option>
              ))}
            </select>
          </label>
        </div>
        <DataState query={q} loadingLabel="Loading incidents">
          {(d) =>
            d.incidents.length === 0 ? (
              <Empty title="No incidents match">Incidents appear when sustained blocking, connection pressure, workload regressions, or retained WAL are detected.</Empty>
            ) : (
              <>
                <div className="overflow-x-auto">
                  <table className="w-full min-w-[760px] text-sm">
                    <caption className="sr-only">Incidents</caption>
                    <thead>
                      <tr className="border-b border-border text-left text-xs text-muted">
                        <th className="py-2 pr-3 font-medium">Incident</th>
                        <th className="py-2 pr-3 font-medium">Target</th>
                        <th className="py-2 pr-3 font-medium">State</th>
                        <th className="py-2 pr-3 font-medium">Opened</th>
                        <th className="py-2 pr-3 font-medium">Duration</th>
                        <th className="py-2 font-medium">Rule</th>
                      </tr>
                    </thead>
                    <tbody>
                      {d.incidents.map((i) => {
                        const end = i.resolved_at ?? i.last_seen_at
                        return (
                          <tr key={i.id} className="border-b border-border">
                            <td className="py-2 pr-3">
                              <Link to={`/incidents/${i.id}`} className="flex items-center gap-2 hover:underline">
                                <SeverityBadge severity={i.severity} />
                                <span>
                                  #{i.id} {i.title}
                                </span>
                              </Link>
                            </td>
                            <td className="py-2 pr-3">{i.target_name}</td>
                            <td className="py-2 pr-3">
                              {i.state === 'open' ? <Tag tone="crit">open</Tag> : <Tag tone="ok">resolved</Tag>}
                              {i.acknowledged_at && <span className="ml-1"><Tag>acknowledged</Tag></span>}
                            </td>
                            <td className="py-2 pr-3 tabular">{fmtTime(i.opened_at)}</td>
                            <td className="py-2 pr-3 tabular">{fmtDuration((Date.parse(end) - Date.parse(i.opened_at)) / 1000)}</td>
                            <td className="py-2">
                              <code className="text-xs">{i.rule_id}</code>
                            </td>
                          </tr>
                        )
                      })}
                    </tbody>
                  </table>
                </div>
                <Pagination page={page} pageSize={d.page_size} total={d.total} onPage={(p) => set('page', String(p))} />
              </>
            )
          }
        </DataState>
      </Card>
    </>
  )
}
