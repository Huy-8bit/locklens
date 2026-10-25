import { useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { Link } from 'react-router'
import { Lock, Play, Square, Workflow } from 'lucide-react'
import { ApiError } from '../../api/client'
import { cancelSql, useCatalog, useExplain, usePolicy, useRunSql } from '../../api/hooks'
import type { GridResult } from '../../api/types'
import { compact, num } from '../../lib/format'
import { CopyButton, highlight } from '../SqlText'
import { trCls } from '../Table'
import { Badge, Button, cx, Empty, ErrorBox, inputCls, selectCls, Spinner, Tabs } from '../ui'
import { records } from './ExplorerPanel'

// Drafts live in memory only: they survive navigation, not a reload, and
// never reach browser storage or the server's store.
const drafts = new Map<number, string>()
const STARTER = '-- Read-only: one statement, at most 500 rows.\nSELECT current_database(), current_user, now();\n'

// ---------------------------------------------------------------------------
// EXPLAIN plan

export interface PlanNode {
  'Node Type': string
  'Relation Name'?: string
  Alias?: string
  'Index Name'?: string
  'Join Type'?: string
  Strategy?: string
  'Startup Cost'?: number
  'Total Cost'?: number
  'Plan Rows'?: number
  'Plan Width'?: number
  'Sort Key'?: string[]
  'Hash Cond'?: string
  'Index Cond'?: string
  Filter?: string
  'Parent Relationship'?: string
  Plans?: PlanNode[]
}

/** The root plan node from an `EXPLAIN (FORMAT JSON)` result, or null. */
export function parsePlan(r: GridResult | undefined): PlanNode | null {
  const text = r?.rows[0]?.[0]
  if (!text) return null
  try {
    const v = JSON.parse(text) as { Plan?: PlanNode }[]
    return v[0]?.Plan ?? null
  } catch {
    return null
  }
}

function nodeTitle(n: PlanNode): string {
  const parts = [n['Join Type'] && n['Node Type'].includes('Join') ? `${n['Join Type']} ${n['Node Type']}` : n['Node Type']]
  if (n.Strategy && n['Node Type'] === 'Aggregate') parts[0] = `${n.Strategy} Aggregate`
  return parts.join(' ')
}

function nodeDetail(n: PlanNode): string | null {
  if (n['Relation Name']) return `on ${n['Relation Name']}${n.Alias && n.Alias !== n['Relation Name'] ? ` ${n.Alias}` : ''}${n['Index Name'] ? ` using ${n['Index Name']}` : ''}`
  if (n['Sort Key']) return `by ${n['Sort Key'].join(', ')}`
  if (n['Hash Cond']) return n['Hash Cond']
  return null
}

const SCAN = /Seq Scan|Index|Bitmap|Function Scan|Values Scan|CTE Scan|Subquery Scan/

function PlanBox({ n, max }: { n: PlanNode; max: number }) {
  const cost = n['Total Cost'] ?? 0
  const share = max > 0 ? cost / max : 0
  const detail = nodeDetail(n)
  const cond = n['Index Cond'] ?? n.Filter
  return (
    <div className={cx('w-52 shrink-0 rounded-xl border bg-surface px-3 py-2 shadow-sm', n['Node Type'] === 'Seq Scan' ? 'border-warn/60' : 'border-line-strong')}>
      <div className="flex items-center gap-1.5 text-sm font-semibold text-ink">
        <span aria-hidden className={cx('h-2 w-2 shrink-0 rounded-full', SCAN.test(n['Node Type']) ? 'bg-[var(--s2)]' : 'bg-[var(--s1)]')} />
        <span className="truncate">{nodeTitle(n)}</span>
      </div>
      {detail && (
        <div className="mt-0.5 truncate font-mono text-[11.5px] text-ink-2" title={detail}>
          {detail}
        </div>
      )}
      {cond && (
        <div className="truncate font-mono text-[11px] text-muted" title={cond}>
          {n['Index Cond'] ? 'cond' : 'filter'}: {cond}
        </div>
      )}
      <div className="mt-1 flex justify-between gap-2 text-[11px] text-muted">
        <span className="num">cost {num(cost, 2)}</span>
        <span className="num">rows {compact(n['Plan Rows'])}</span>
      </div>
      <div className="mt-1 h-1 overflow-hidden rounded-full bg-surface-3" title="Share of the root's estimated total cost">
        <div className="h-full rounded-full bg-[var(--s1)]" style={{ width: `${Math.max(2, share * 100)}%` }} />
      </div>
    </div>
  )
}

/** Horizontal plan tree: the root on the left, inputs to its right. */
function PlanTree({ n, max }: { n: PlanNode; max: number }) {
  const kids = n.Plans ?? []
  return (
    <div className="flex items-center">
      <PlanBox n={n} max={max} />
      {kids.length > 0 && (
        <>
          <span aria-hidden className="h-0.5 w-5 shrink-0 bg-line-strong" />
          <div className={cx('flex flex-col gap-3', kids.length > 1 && 'border-l-2 border-line-strong')}>
            {kids.map((k, i) => (
              <div key={i} className="flex items-center">
                <span aria-hidden className={cx('h-0.5 shrink-0 bg-line-strong', kids.length > 1 ? 'w-4' : 'w-0')} />
                <PlanTree n={k} max={max} />
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  )
}

function planOutline(n: PlanNode, depth = 0): string {
  const d = nodeDetail(n)
  const line = `${'  '.repeat(depth)}${depth ? '-> ' : ''}${nodeTitle(n)}${d ? ` ${d}` : ''}  (cost=${n['Startup Cost'] ?? '?'}..${n['Total Cost'] ?? '?'} rows=${n['Plan Rows'] ?? '?'} width=${n['Plan Width'] ?? '?'})`
  return [line, ...(n.Plans ?? []).map((k) => planOutline(k, depth + 1))].join('\n')
}

export function PlanView({ result }: { result: GridResult }) {
  const plan = parsePlan(result)
  if (!plan) return <p className="text-sm text-muted">The plan could not be read.</p>
  const outline = planOutline(plan)
  return (
    <div className="space-y-3">
      <div className="overflow-x-auto pb-2" role="img" aria-label={`Execution plan: ${outline}`}>
        <PlanTree n={plan} max={plan['Total Cost'] ?? 0} />
      </div>
      <details className="text-sm">
        <summary className="cursor-pointer text-xs font-medium text-ink-2">Plan as text</summary>
        <div className="mt-2 flex justify-end">
          <CopyButton text={outline} />
        </div>
        <pre className="overflow-x-auto rounded-lg bg-surface-2 p-3 font-mono text-xs text-ink-2">{outline}</pre>
      </details>
      <p className="text-xs text-muted">Estimates from EXPLAIN without ANALYZE: the statement was planned, not executed.</p>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Editor

function lineCol(sql: string, pos: number): string {
  const before = sql.slice(0, Math.max(0, pos - 1))
  const lines = before.split('\n')
  return `line ${lines.length}, column ${(lines[lines.length - 1]?.length ?? 0) + 1}`
}

function Editor({ value, onChange, onRun, label }: { value: string; onChange: (v: string) => void; onRun: () => void; label: string }) {
  const lines = value.split('\n')
  const longest = Math.max(...lines.map((l) => l.length), 20)
  const nodes = useMemo(() => highlight(value), [value])
  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault()
      onRun()
    } else if (e.key === 'Tab' && !e.shiftKey) {
      e.preventDefault()
      const t = e.currentTarget
      const { selectionStart: s, selectionEnd: end } = t
      onChange(value.slice(0, s) + '  ' + value.slice(end))
      requestAnimationFrame(() => t.setSelectionRange(s + 2, s + 2))
    }
  }
  const h = Math.max(6, lines.length + 1) * 24 + 16
  return (
    <div className="max-h-[360px] overflow-auto rounded-xl border border-line-strong bg-surface-2 focus-within:border-accent">
      <div className="flex min-w-full font-mono text-[13px] leading-6" style={{ height: h }}>
        <div aria-hidden className="sticky left-0 shrink-0 select-none border-r border-line bg-surface-2 py-2 pr-3 pl-3 text-right text-muted">
          {lines.map((_, i) => (
            <div key={i}>{i + 1}</div>
          ))}
        </div>
        <div className="relative flex-1" style={{ minWidth: `${longest + 4}ch` }}>
          <pre aria-hidden className="pointer-events-none absolute inset-0 m-0 overflow-hidden whitespace-pre px-3 py-2 text-ink">
            {nodes}
            {'\n'}
          </pre>
          <textarea
            aria-label={label}
            spellCheck={false}
            autoCapitalize="off"
            autoComplete="off"
            wrap="off"
            value={value}
            onChange={(e) => onChange(e.target.value)}
            onKeyDown={onKey}
            className="absolute inset-0 h-full w-full resize-none overflow-hidden whitespace-pre bg-transparent px-3 py-2 text-transparent caret-[var(--ink)] outline-none selection:bg-[var(--accent-soft)] selection:text-transparent"
          />
        </div>
      </div>
    </div>
  )
}

function ResultGrid({ r }: { r: GridResult }) {
  if (r.columns.length === 0) return <p className="text-sm text-muted">The statement returned no columns.</p>
  return (
    <div>
      <div className="max-h-[360px] overflow-auto rounded-lg border border-line">
        <table className="w-full border-collapse text-[13px]">
          <caption className="sr-only">Statement result</caption>
          <thead className="sticky top-0 bg-surface">
            <tr>
              <th scope="col" className="relative w-10 border-b border-line px-2 py-2 text-right text-xs font-medium text-muted">
                #
              </th>
              {r.columns.map((c, i) => (
                <th key={i} scope="col" className="relative whitespace-nowrap border-b border-line px-3 py-2 text-left text-xs font-medium text-muted">
                  {c.name}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {r.rows.slice(0, 500).map((row, ri) => (
              <tr key={ri} className={trCls}>
                <td className="num px-2 py-1.5 text-right text-xs text-muted">{ri + 1}</td>
                {row.map((v, ci) => (
                  <td key={ci} className="max-w-[320px] truncate px-3 py-1.5 font-mono text-xs" title={v ?? 'NULL'}>
                    {v === null ? <span className="italic text-muted">NULL</span> : v}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="mt-2 text-xs text-muted">
        {num(Math.min(r.rows.length, 500))} row{r.rows.length === 1 ? '' : 's'} · {r.duration_ms} ms
        {r.truncated ? ' · limited to the first 500 rows or 1 MiB' : ''}
        {r.cell_truncated ? ' · long values truncated' : ''}
      </p>
    </div>
  )
}

interface Message {
  at: Date
  tone: 'good' | 'crit' | 'muted'
  text: string
}

type ResultTab = 'results' | 'plan' | 'messages'

/**
 * Read-only SQL editor. Statements run on the instance with its separate
 * browsing role, inside a read-only transaction that is always rolled back,
 * with bounded rows, bytes, and time. EXPLAIN plans without executing.
 */
export function SqlPanel({ id, toolbar, compactView = false }: { id: number; toolbar?: ReactNode; compactView?: boolean }) {
  const policy = usePolicy(id)
  const p = policy.data
  const canMeta = !!p?.configured && p.metadata_enabled
  const schemas = useCatalog(id, canMeta, 'schemas')
  const schemaList = schemas.data ? records(schemas.data).map((r) => r.schema ?? '') : []
  const [schema, setSchema] = useState('')
  const activeSchema = schema || (schemaList.includes('public') || schemaList.length === 0 ? 'public' : (schemaList[0] ?? 'public'))
  const [sql, setSqlState] = useState(() => drafts.get(id) ?? STARTER)
  const setSql = (v: string) => {
    drafts.set(id, v)
    setSqlState(v)
  }
  const [tab, setTab] = useState<ResultTab>('results')
  const [result, setResult] = useState<GridResult | null>(null)
  const [plan, setPlan] = useState<GridResult | null>(null)
  const [error, setError] = useState<{ e: unknown; sql: string } | null>(null)
  const [messages, setMessages] = useState<Message[]>([])
  const run = useRunSql(id)
  const explain = useExplain(id)
  const inflight = useRef<{ ctrl: AbortController; requestId: string } | null>(null)
  const busy = run.isPending || explain.isPending

  const log = (tone: Message['tone'], text: string) => setMessages((m) => [{ at: new Date(), tone, text }, ...m].slice(0, 50))
  const statement = () => sql.trim().replace(/;\s*$/, '')

  const execute = (mode: 'run' | 'explain') => {
    const text = statement()
    if (!text || busy) return
    const ctrl = new AbortController()
    const requestId = crypto.randomUUID()
    inflight.current = { ctrl, requestId }
    setError(null)
    const onError = (e: unknown) => {
      if (ctrl.signal.aborted) {
        log('muted', 'Cancelled.')
        return
      }
      setError({ e, sql: text })
      const pos = e instanceof ApiError ? Number(e.fields.position) : NaN
      log('crit', `${e instanceof Error ? e.message : String(e)}${e instanceof ApiError && e.code ? ` (${e.code})` : ''}${pos > 0 ? ` at ${lineCol(text, pos)}` : ''}`)
      setTab('messages')
    }
    if (mode === 'run') {
      run.mutate(
        { sql: text, schema: activeSchema, requestId, signal: ctrl.signal },
        {
          onSuccess: (r) => {
            setResult(r)
            setTab('results')
            log('good', `Returned ${num(r.rows.length)} row(s) in ${r.duration_ms} ms${r.truncated ? ' (truncated)' : ''}.`)
          },
          onError,
        },
      )
    } else {
      explain.mutate(
        { sql: text, schema: activeSchema, signal: ctrl.signal },
        {
          onSuccess: (r) => {
            setPlan(r)
            setTab('plan')
            log('good', `Planned in ${r.duration_ms} ms (EXPLAIN without ANALYZE).`)
          },
          onError,
        },
      )
    }
  }

  const cancel = () => {
    const f = inflight.current
    if (!f) return
    f.ctrl.abort()
    void cancelSql(id, f.requestId).catch(() => undefined)
  }

  if (!p) return <div className="px-5 pb-5">{policy.error ? <ErrorBox error={policy.error} title="Could not check access" /> : <Spinner label="Checking access" />}</div>
  if (!p.configured || !p.sql_enabled) {
    return (
      <div className="space-y-3 px-5 pb-5">
        {toolbar && <div className="flex flex-wrap gap-2">{toolbar}</div>}
        <Empty title="The SQL editor is off for this instance" icon={<Lock aria-hidden className="h-5 w-5" />}>
          It runs read-only statements with a separate role, never the monitoring role. Enable “Read-only SQL editor” in the{' '}
          <Link to={`/explorer/${id}?access=1`} className="text-accent hover:underline">
            access settings
          </Link>
          .
        </Empty>
      </div>
    )
  }

  return (
    <div className="space-y-3 px-5 pb-5">
      <div className="flex flex-wrap items-center gap-2">
        {toolbar}
        <label className="sr-only" htmlFor={`sql-schema-${id}`}>
          Schema
        </label>
        {canMeta ? (
          <select id={`sql-schema-${id}`} className={`${selectCls} min-w-32`} value={activeSchema} onChange={(e) => setSchema(e.target.value)}>
            {(schemaList.length ? schemaList : ['public']).map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
        ) : (
          <input id={`sql-schema-${id}`} className={`${inputCls} w-32 font-mono`} value={activeSchema} onChange={(e) => setSchema(e.target.value)} maxLength={63} />
        )}
        <div className="ml-auto flex gap-2">
          {busy ? (
            <Button size="sm" variant="danger" onClick={cancel}>
              <Square aria-hidden className="h-3.5 w-3.5" /> Cancel
            </Button>
          ) : null}
          <Button size="sm" variant="primary" busy={run.isPending} disabled={busy} onClick={() => execute('run')} title="Run (Ctrl/⌘ + Enter)">
            <Play aria-hidden className="h-3.5 w-3.5" /> Run
          </Button>
          <Button size="sm" busy={explain.isPending} disabled={busy} onClick={() => execute('explain')}>
            <Workflow aria-hidden className="h-3.5 w-3.5" /> Explain
          </Button>
        </div>
      </div>
      <Editor value={sql} onChange={setSql} onRun={() => execute('run')} label="SQL statement" />
      <div>
        <Tabs
          label="Statement output"
          value={tab}
          onChange={setTab}
          items={[
            { value: 'results', label: 'Results' },
            { value: 'plan', label: 'Execution plan' },
            { value: 'messages', label: 'Messages', count: messages.length || undefined },
          ]}
        />
        <div className={cx('pt-3', compactView && 'max-h-[420px] overflow-y-auto')}>
          {tab === 'results' ? (
            run.isPending ? (
              <Spinner label="Running" />
            ) : result ? (
              <ResultGrid r={result} />
            ) : (
              <p className="text-sm text-muted">Run a statement to see its rows. Press Ctrl/⌘ + Enter in the editor.</p>
            )
          ) : tab === 'plan' ? (
            explain.isPending ? (
              <Spinner label="Planning" />
            ) : plan ? (
              <PlanView result={plan} />
            ) : (
              <p className="text-sm text-muted">Choose Explain to see how PostgreSQL would execute the statement.</p>
            )
          ) : (
            <div className="space-y-2">
              {error && <ErrorBox error={error.e} title="PostgreSQL did not run the statement" />}
              {messages.length === 0 ? (
                <p className="text-sm text-muted">No messages yet.</p>
              ) : (
                <ul className="space-y-1 text-sm">
                  {messages.map((m, i) => (
                    <li key={i} className="flex gap-2">
                      <span className="num shrink-0 text-xs leading-5 text-muted">{m.at.toLocaleTimeString('en-GB')}</span>
                      <Badge tone={m.tone}>{m.tone === 'good' ? 'ok' : m.tone === 'crit' ? 'error' : 'info'}</Badge>
                      <span className="min-w-0 break-words text-ink-2">{m.text}</span>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
