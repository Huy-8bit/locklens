import { useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { Search } from 'lucide-react'
import { useFleetLive, useFleetQueries } from '../api/hooks'
import { PageHeader } from '../components/Shell'
import { SqlText } from '../components/SqlText'
import { tableCls, tdCls, tdNum, Th, trCls } from '../components/Table'
import { Card, Empty, inputCls, Load, Pills, selectCls } from '../components/ui'
import { commandColor, COMMAND_LABEL, COMMANDS } from '../lib/commands'
import { compact, ms, pct, rate } from '../lib/format'
import { rangeLabel, useScope } from '../lib/scope'

type Sort = 'total_time' | 'calls' | 'mean_time' | 'rows'
const SORTS: Sort[] = ['total_time', 'calls', 'mean_time', 'rows']

/** Statements across all instances of the environment, ranked together. */
export default function FleetQueriesPage() {
  const { env, range } = useScope()
  const [params, setParams] = useSearchParams()
  const sort = (SORTS as string[]).includes(params.get('sort') ?? '') ? (params.get('sort') as Sort) : 'total_time'
  const command = params.get('command') ?? ''
  const [term, setTerm] = useState(params.get('q') ?? '')
  const q = term.trim().length >= 3 ? term.trim() : ''
  const queries = useFleetQueries(env, range, sort, 50, q, command)
  const live = useFleetLive(env)
  const set = (k: string, v: string) =>
    setParams(
      (p) => {
        if (v) p.set(k, v)
        else p.delete(k)
        return p
      },
      { replace: true },
    )
  const multi = (live.data?.targets.length ?? 0) > 1

  return (
    <>
      <PageHeader title="Queries" subtitle={`Statements from pg_stat_statements across instances · ${rangeLabel(range).toLowerCase()}. Monitoring's own statements are excluded.`} />
      <div className="space-y-4">
        <Card pad={false}>
          <div className="flex flex-wrap items-center gap-2 px-5 pt-4 pb-3">
            <Pills label="Rank queries by" value={sort} onChange={(v) => set('sort', v === 'total_time' ? '' : v)} items={[{ value: 'total_time', label: 'Total time' }, { value: 'calls', label: 'Calls' }, { value: 'mean_time', label: 'Mean latency' }, { value: 'rows', label: 'Rows' }]} />
            <label htmlFor="fq-command" className="sr-only">
              Statement type
            </label>
            <select id="fq-command" className={selectCls} value={command} onChange={(e) => set('command', e.target.value)}>
              <option value="">All statement types</option>
              {COMMANDS.map((c) => (
                <option key={c} value={c}>
                  {COMMAND_LABEL[c]}
                </option>
              ))}
            </select>
            <div className="relative ml-auto w-full sm:w-72">
              <Search aria-hidden className="pointer-events-none absolute top-2 left-2.5 h-4 w-4 text-muted" />
              <input
                aria-label="Search SQL text"
                placeholder="Search SQL text (3+ characters)…"
                className={`${inputCls} pl-8`}
                value={term}
                onChange={(e) => {
                  setTerm(e.target.value)
                  set('q', e.target.value.trim().length >= 3 ? e.target.value.trim() : '')
                }}
              />
            </div>
          </div>
          <Load q={queries} label="Loading queries">
            {(d) =>
              d.items.length === 0 ? (
                <div className="px-5 pb-5">
                  <Empty title="No statements match">Try another statement type, a longer time range, or clear the search.</Empty>
                </div>
              ) : (
                <div className="overflow-x-auto">
                  <table className={tableCls}>
                    <caption className="sr-only">Queries across instances</caption>
                    <thead>
                      <tr>
                        <Th className="w-[44%] pl-5">Query</Th>
                        {multi && <Th>Instance</Th>}
                        <Th>Type</Th>
                        <Th align="right">Calls</Th>
                        <Th align="right">Calls/s</Th>
                        <Th align="right">Total time</Th>
                        <Th align="right">Mean</Th>
                        <Th align="right">Share</Th>
                        <Th align="right" className="pr-5">
                          Rows
                        </Th>
                      </tr>
                    </thead>
                    <tbody>
                      {d.items.map((it) => (
                        <tr key={`${it.target_id}-${it.identity_id}`} className={trCls}>
                          <td className={`${tdCls} max-w-[520px] pl-5`}>
                            <Link to={`/queries/${it.target_id}/${it.identity_id}`} className="block hover:underline">
                              {it.query_text ? <SqlText sql={it.query_text} clamp /> : <span className="font-mono text-xs text-muted">queryid {it.queryid}</span>}
                            </Link>
                            <div className="text-xs text-muted">
                              {it.datname} · {it.username}
                            </div>
                          </td>
                          {multi && (
                            <td className={`${tdCls} whitespace-nowrap text-xs`}>
                              <Link to={`/databases/${it.target_id}/queries`} className="hover:underline">
                                {it.target_name}
                              </Link>
                            </td>
                          )}
                          <td className={`${tdCls} whitespace-nowrap text-xs`}>
                            <span className="inline-flex items-center gap-1.5">
                              <span aria-hidden className="h-2 w-2 rounded-sm" style={{ background: commandColor(it.command) }} />
                              {COMMAND_LABEL[it.command] ?? it.command}
                            </span>
                          </td>
                          <td className={tdNum}>{compact(it.calls)}</td>
                          <td className={tdNum}>{rate(it.calls_per_second, '')}</td>
                          <td className={tdNum}>{ms(it.total_exec_ms)}</td>
                          <td className={tdNum}>{ms(it.mean_exec_ms)}</td>
                          <td className={tdNum} title="Share of its instance's statement time">
                            {pct(it.share_of_total_time, 0)}
                          </td>
                          <td className={`${tdNum} pr-5`}>{compact(it.rows)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                  <p className="px-5 py-3 text-xs text-muted">Top 50 by the selected ranking. Each instance's full list, with trends and per-type charts, is under Databases → Queries.</p>
                </div>
              )
            }
          </Load>
        </Card>
      </div>
    </>
  )
}
