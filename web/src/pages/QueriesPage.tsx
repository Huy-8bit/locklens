import { Link, useSearchParams } from 'react-router'
import { Search } from 'lucide-react'
import { useCommands, useQueries, type QueryParams } from '../api/hooks'
import { CommandMix, CommandsChart } from '../components/Commands'
import { SectionHeader, useNumParam } from '../components/Shell'
import { SqlText } from '../components/SqlText'
import { Pager, tableCls, tdCls, tdNum, Th, trCls } from '../components/Table'
import { Badge, Card, Empty, inputCls, Load, Notice, Stat } from '../components/ui'
import { commandColor, COMMAND_LABEL, COMMANDS } from '../lib/commands'
import { compact, ms, num, pct, rate, seconds } from '../lib/format'
import { useRange } from '../lib/scope'

function useQueryState(): [QueryParams, (patch: Partial<QueryParams>) => void] {
  const [params, setParams] = useSearchParams()
  const [range] = useRange()
  const state: QueryParams = {
    range,
    sort: params.get('sort') ?? 'total_time',
    order: params.get('order') === 'asc' ? 'asc' : 'desc',
    page: Math.max(1, Number(params.get('page')) || 1),
    q: params.get('q') ?? '',
    command: params.get('command') ?? '',
    compare: params.get('compare') === '1',
    includeMonitoring: params.get('monitoring') === '1',
  }
  const set = (patch: Partial<QueryParams>) =>
    setParams(
      (p) => {
        const n = new URLSearchParams(p)
        const next = { ...state, ...patch }
        const put = (k: string, v: string, def: string) => (v === def ? n.delete(k) : n.set(k, v))
        put('sort', next.sort, 'total_time')
        put('order', next.order, 'desc')
        put('q', next.q, '')
        put('command', next.command, '')
        put('compare', next.compare ? '1' : '', '')
        put('monitoring', next.includeMonitoring ? '1' : '', '')
        // Any change other than paging returns to the first page.
        put('page', String('page' in patch ? next.page : 1), '1')
        return n
      },
      { replace: true },
    )
  return [state, set]
}

export default function QueriesPage() {
  const id = useNumParam('id')
  const [range] = useRange()
  const [st, set] = useQueryState()
  const commands = useCommands(id, range, st.includeMonitoring)
  const list = useQueries(id, st)
  const sortBy = (key: string) => set({ sort: key, order: st.sort === key && st.order === 'desc' ? 'asc' : 'desc' })

  return (
    <>
      <SectionHeader title="Queries" subtitle="Every statement the server executed (pg_stat_statements), grouped by type, with its full SQL." />
      <Load q={commands} label="Loading workload">
        {(c) => {
          if (c.available === false) return <Notice tone="warn">{c.unavailable_reason || 'pg_stat_statements is not available on this instance.'} Query statistics need the extension in shared_preload_libraries and CREATE EXTENSION in the monitored database.</Notice>
          const calls = c.totals.reduce((n, t) => n + t.calls, 0)
          const time = c.totals.reduce((n, t) => n + t.total_exec_ms, 0)
          const statements = c.totals.reduce((n, t) => n + t.statements, 0)
          const covered = c.coverage.covered_seconds
          return (
            <div className="space-y-3">
              <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-5">
                <Stat label="Executions" value={compact(calls)} sub={`${num(statements)} distinct statements`} />
                <Stat label="Average per second" value={covered > 0 ? rate(calls / covered, '') : '—'} sub="over the selected range" />
                <Stat label="Total execution time" value={ms(time)} sub="includes time waiting" />
                <Stat label="Average per query" value={ms(calls ? time / calls : null)} sub="mean over all executions" />
                <Stat label="Data coverage" value={pct(c.coverage.window_seconds ? covered / c.coverage.window_seconds : null, 0)} sub={`${num(c.coverage.collections)} samples${c.coverage.resets ? ` · ${c.coverage.resets} resets` : ''}${c.coverage.evicted_entries ? ` · ${num(c.coverage.evicted_entries)} evicted` : ''}`} />
              </div>
              <div className="grid gap-3 xl:grid-cols-2">
                <Card title="How each type of query runs over time" subtitle={c.monitoring_hidden ? `Statements of the monitoring role ${c.monitoring_user} are hidden.` : 'Including the monitoring role.'}>
                  <CommandsChart data={c} height={260} />
                </Card>
                <Card title="Breakdown by type" subtitle="Click a type to filter the statement list">
                  <CommandMix data={c} active={st.command} onPick={(cmd) => set({ command: cmd })} />
                </Card>
              </div>
            </div>
          )
        }}
      </Load>

      <Card className="mt-3" pad={false} title="Statements" subtitle="Normalised SQL: literal values are replaced by $1, $2, …">
        <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 pb-3">
          <form
            role="search"
            className="relative"
            onSubmit={(e) => {
              e.preventDefault()
              set({ q: String(new FormData(e.currentTarget).get('q') ?? '').trim() })
            }}
          >
            <Search aria-hidden className="pointer-events-none absolute top-2 left-2.5 h-4 w-4 text-muted" />
            <input key={st.q} name="q" aria-label="Search statements" placeholder="Search SQL, user, database" className={`${inputCls} w-72 pl-8`} defaultValue={st.q} />
          </form>
          <div className="flex flex-wrap gap-1" role="group" aria-label="Statement type">
            {['', ...COMMANDS.filter((c) => c === st.command || commands.data?.totals.some((t) => t.command === c))].map((cmd) => (
              <button
                key={cmd || 'all'}
                type="button"
                aria-pressed={st.command === cmd}
                onClick={() => set({ command: cmd })}
                className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-xs ${st.command === cmd ? 'border-accent bg-accent-soft text-accent' : 'border-line-strong text-ink-2 hover:text-ink'}`}
              >
                {cmd && <span aria-hidden className="h-2 w-2 rounded-sm" style={{ background: commandColor(cmd) }} />}
                {cmd ? (COMMAND_LABEL[cmd] ?? cmd).replace(/ \(.*\)/, '') : 'All types'}
              </button>
            ))}
          </div>
          <label className="ml-auto inline-flex items-center gap-1.5 text-xs text-ink-2">
            <input type="checkbox" checked={st.compare} onChange={(e) => set({ compare: e.target.checked })} />
            Compare with previous period
          </label>
          <label className="inline-flex items-center gap-1.5 text-xs text-ink-2">
            <input type="checkbox" checked={st.includeMonitoring} onChange={(e) => set({ includeMonitoring: e.target.checked })} />
            Include LockLens' own queries
          </label>
        </div>
        <Load q={list} label="Loading statements">
          {(d) =>
            d.items.length === 0 ? (
              <div className="p-4">
                <Empty title="No statements match">Try another type, a longer time range, or clear the search.</Empty>
              </div>
            ) : (
              <>
                {!d.query_text_enabled && <div className="px-4 pt-3"><Notice>SQL text collection is switched off for this instance; statements are shown by query ID. Enable it in Settings.</Notice></div>}
                <div className="overflow-x-auto">
                  <table className={tableCls}>
                    <caption className="sr-only">Statements</caption>
                    <thead>
                      <tr>
                        <Th className="w-[46%]">Statement</Th>
                        <Th align="right" sortKey="calls" sort={st.sort} order={st.order} onSort={sortBy}>Calls</Th>
                        <Th align="right">Per second</Th>
                        <Th align="right" sortKey="total_time" sort={st.sort} order={st.order} onSort={sortBy}>Total time</Th>
                        <Th align="right" sortKey="mean_time" sort={st.sort} order={st.order} onSort={sortBy}>Avg time</Th>
                        <Th align="right" sortKey="rows" sort={st.sort} order={st.order} onSort={sortBy}>Rows</Th>
                        {st.compare && <Th align="right" sortKey="mean_ratio" sort={st.sort} order={st.order} onSort={sortBy}>Avg vs before</Th>}
                      </tr>
                    </thead>
                    <tbody>
                      {d.items.map((it) => (
                        <tr key={it.identity_id} className={trCls}>
                          <td className={`${tdCls} max-w-0`}>
                            <Link to={`/queries/${id}/${it.identity_id}`} className="block hover:underline">
                              {it.query_text ? <SqlText sql={it.query_text} clamp /> : <span className="font-mono text-xs">queryid {it.queryid}</span>}
                            </Link>
                            <div className="mt-1 flex flex-wrap items-center gap-1.5 text-xs text-muted">
                              <span className="inline-flex items-center gap-1">
                                <span aria-hidden className="h-2 w-2 rounded-sm" style={{ background: commandColor(it.command) }} />
                                {(COMMAND_LABEL[it.command] ?? it.command).replace(/ \(.*\)/, '')}
                              </span>
                              <span>· {it.datname} · {it.username}</span>
                              {it.share_of_total_time !== null && <span>· {pct(it.share_of_total_time, 1)} of time</span>}
                              {it.comparison?.mean_regression && <Badge tone="warn">slower</Badge>}
                              {it.comparison?.volume_increase && <Badge tone="accent">more calls</Badge>}
                            </div>
                          </td>
                          <td className={tdNum}>{compact(it.calls)}</td>
                          <td className={tdNum}>{rate(it.calls_per_second, '')}</td>
                          <td className={tdNum}>{ms(it.total_exec_ms)}</td>
                          <td className={tdNum}>{ms(it.mean_exec_ms)}</td>
                          <td className={tdNum}>{compact(it.rows)}</td>
                          {st.compare && (
                            <td className={tdNum}>
                              {it.comparison?.too_few_calls ? <span className="text-xs text-muted">too few calls</span> : it.comparison?.mean_ratio != null ? `${it.comparison.mean_ratio.toFixed(2)}×` : '—'}
                            </td>
                          )}
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                <Pager page={st.page} pageSize={d.page_size} total={d.total} onPage={(p) => set({ page: p })} />
                <p className="px-4 pb-3 text-xs text-muted">
                  Coverage {seconds(d.coverage.covered_seconds)} of {seconds(d.coverage.window_seconds)}. Averages are per window; pg_stat_statements keeps no per-execution samples.
                </p>
              </>
            )
          }
        </Load>
      </Card>
    </>
  )
}
