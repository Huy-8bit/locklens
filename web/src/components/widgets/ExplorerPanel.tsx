import { useMemo, useState, type FormEvent, type ReactNode } from 'react'
import { Link } from 'react-router'
import { Braces, ChevronDown, ChevronRight, Eye, FunctionSquare, KeyRound, Lock, Search, Shapes, Table2 } from 'lucide-react'
import { useCatalog, usePolicy, useSavePolicy, useTableData, type DataRequest } from '../../api/hooks'
import type { GridResult, WorkspacePolicy } from '../../api/types'
import { ago, bytes, compact } from '../../lib/format'
import { CopyButton } from '../SqlText'
import { tableCls, tdCls, tdNum, Th, trCls } from '../Table'
import { Badge, Button, Card, Confirm, cx, Drawer, Empty, ErrorBox, Field, inputCls, KeyValue, Load, Notice, selectCls, Spinner, Tabs } from '../ui'

/** Rows of a bounded result as objects keyed by column name. */
export function records(r: GridResult): Record<string, string | null>[] {
  return r.rows.map((row) => Object.fromEntries(r.columns.map((c, i) => [c.name, row[i] ?? null])))
}

export const KIND: Record<string, string> = { r: 'table', p: 'partitioned table', v: 'view', m: 'materialized view', f: 'foreign table' }

// ---------------------------------------------------------------------------
// Access setup

export function AccessSetup({ id, policy }: { id: number; policy: WorkspacePolicy }) {
  const save = useSavePolicy(id)
  const [dsn, setDsn] = useState('')
  const [metadata, setMetadata] = useState(policy.metadata_enabled || !policy.configured)
  const [data, setData] = useState(policy.data_enabled)
  const [sql, setSql] = useState(policy.sql_enabled)
  const [confirm, setConfirm] = useState(false)
  const apply = () => save.mutate({ metadata_enabled: metadata, data_enabled: data, sql_enabled: sql, dsn: dsn || undefined })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if ((data && !policy.data_enabled) || (sql && !policy.sql_enabled)) setConfirm(true)
    else apply()
  }
  return (
    <Card title="Set up data access" subtitle="Browsing tables and the SQL editor use a separate PostgreSQL role, not the monitoring role.">
      <form onSubmit={submit} className="max-w-2xl space-y-3">
        <Notice>
          Create a login role with <code>USAGE</code> on the schemas and <code>SELECT</code> on the tables you want to browse, and nothing else (no superuser, no write privileges). LockLens connects with it only on demand, in read-only transactions with short timeouts, and never runs <code>COUNT(*)</code> or writes.
        </Notice>
        <pre className="overflow-x-auto rounded-lg bg-surface-2 p-3 font-mono text-xs text-ink-2">{`CREATE ROLE locklens_reader LOGIN PASSWORD '…';
GRANT USAGE ON SCHEMA public TO locklens_reader;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO locklens_reader;`}</pre>
        <Field label="Connection string of the browsing role" hint={policy.configured ? 'Leave empty to keep the stored credentials. Host, port, and database must match this instance.' : 'Same host, port, and database as this instance, with the separate role. Stored encrypted; never shown again.'} error={save.error instanceof Error ? save.error.message : undefined}>
          <input type="password" autoComplete="off" className={`${inputCls} font-mono`} placeholder="postgres://locklens_reader:…@host:5432/db" value={dsn} onChange={(e) => setDsn(e.target.value)} />
        </Field>
        <label className="flex items-start gap-2 text-sm">
          <input type="checkbox" className="mt-1" checked={metadata} onChange={(e) => setMetadata(e.target.checked)} />
          <span>
            <span className="font-medium">Browse structure</span>
            <span className="block text-xs text-muted">Schemas, tables, views, functions, types, columns, indexes, triggers.</span>
          </span>
        </label>
        <label className="flex items-start gap-2 text-sm">
          <input type="checkbox" className="mt-1" checked={data} onChange={(e) => setData(e.target.checked)} />
          <span>
            <span className="font-medium">Browse table rows</span>
            <span className="block text-xs text-muted">Read actual data, at most 100 rows per page. Rows can contain personal data.</span>
          </span>
        </label>
        <label className="flex items-start gap-2 text-sm">
          <input type="checkbox" className="mt-1" checked={sql} onChange={(e) => setSql(e.target.checked)} />
          <span>
            <span className="font-medium">Read-only SQL editor</span>
            <span className="block text-xs text-muted">Run one read-only statement at a time (at most 500 rows, 1 MiB) and show EXPLAIN plans. Statements run in a read-only transaction that is always rolled back.</span>
          </span>
        </label>
        <Button type="submit" variant="primary" busy={save.isPending}>
          Save access
        </Button>
      </form>
      <Confirm
        open={confirm}
        title="Allow reading data?"
        confirmLabel="Allow"
        busy={save.isPending}
        onCancel={() => setConfirm(false)}
        onConfirm={() => {
          setConfirm(false)
          apply()
        }}
      >
        Anyone signed in to LockLens can then read rows of every table the browsing role can SELECT{sql ? ', including with their own SELECT statements' : ''}. Each access is audited (without the data itself).
      </Confirm>
    </Card>
  )
}

// ---------------------------------------------------------------------------
// Table data grid

export function DataGrid({ id, schema, table, pk, compactView = false }: { id: number; schema: string; table: string; pk: string | null; compactView?: boolean }) {
  const [sort, setSort] = useState<{ col: string; desc: boolean } | null>(null)
  const [filter, setFilter] = useState<{ col: string; op: string; value: string } | null>(null)
  const [draft, setDraft] = useState({ col: '', op: 'contains', value: '' })
  const [limit, setLimit] = useState(compactView ? 10 : 25)
  const [offset, setOffset] = useState(0)
  const [cursors, setCursors] = useState<(string | undefined)[]>([undefined])
  const [cell, setCell] = useState<{ col: string; value: string | null } | null>(null)

  const req: DataRequest & { after?: string } = {
    schema,
    relation: table,
    sort: sort?.col,
    desc: sort?.desc,
    filter_column: filter?.col,
    filter_op: filter?.op,
    filter_value: filter?.value,
    limit,
    offset,
    after: cursors[cursors.length - 1],
  }
  const q = useTableData(id, true, req)
  const keyset = q.data?.pagination === 'primary-key cursor'
  const page = keyset ? cursors.length : Math.floor(offset / limit) + 1
  const reset = () => {
    setOffset(0)
    setCursors([undefined])
  }
  const sortBy = (col: string) => {
    setSort((s) => (s?.col === col ? { col, desc: !s.desc } : { col, desc: false }))
    reset()
  }
  const cols = q.data?.columns ?? []

  return (
    <div>
      {!compactView && (
        <form
          className="flex flex-wrap items-end gap-2 border-b border-line px-4 pb-3"
          onSubmit={(e) => {
            e.preventDefault()
            setFilter(draft.col ? { ...draft } : null)
            reset()
          }}
        >
          <label className="text-xs text-muted">
            Filter column
            <select className={`${inputCls} mt-0.5 block w-44`} value={draft.col} onChange={(e) => setDraft({ ...draft, col: e.target.value })}>
              <option value="">No filter</option>
              {cols.map((c) => (
                <option key={c.name} value={c.name}>
                  {c.name}
                </option>
              ))}
            </select>
          </label>
          <label className="text-xs text-muted">
            Condition
            <select className={`${inputCls} mt-0.5 block w-36`} value={draft.op} onChange={(e) => setDraft({ ...draft, op: e.target.value })}>
              <option value="contains">contains</option>
              <option value="eq">equals</option>
              <option value="null">is NULL</option>
              <option value="not_null">is not NULL</option>
            </select>
          </label>
          {(draft.op === 'contains' || draft.op === 'eq') && (
            <label className="text-xs text-muted">
              Value
              <input className={`${inputCls} mt-0.5 block w-48`} value={draft.value} onChange={(e) => setDraft({ ...draft, value: e.target.value })} maxLength={1024} />
            </label>
          )}
          <Button type="submit" size="sm">
            <Search aria-hidden className="h-3.5 w-3.5" /> Apply
          </Button>
          {filter && (
            <Button
              size="sm"
              variant="ghost"
              onClick={() => {
                setFilter(null)
                setDraft({ col: '', op: 'contains', value: '' })
                reset()
              }}
            >
              Clear
            </Button>
          )}
          <label className="ml-auto text-xs text-muted">
            Rows per page
            <select
              className={`${inputCls} mt-0.5 block w-20`}
              value={limit}
              onChange={(e) => {
                setLimit(Number(e.target.value))
                reset()
              }}
            >
              {[25, 50, 100].map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </label>
        </form>
      )}
      {q.error ? (
        <div className="p-4">
          <ErrorBox error={q.error} title="Could not read rows" />
        </div>
      ) : !q.data ? (
        <div className="px-4">
          <Spinner label="Reading rows" />
        </div>
      ) : q.data.rows.length === 0 ? (
        <div className="p-4">
          <Empty title={filter ? 'No rows match the filter' : 'This table is empty'} />
        </div>
      ) : (
        <>
          <div className={cx('overflow-x-auto', q.isFetching && 'opacity-60')}>
            <table className="w-full border-collapse text-[13px]">
              <caption className="sr-only">
                Rows of {schema}.{table}
              </caption>
              <thead className="sticky top-0 bg-surface">
                <tr>
                  {cols.map((c) => (
                    <th key={c.name} scope="col" aria-sort={sort?.col === c.name ? (sort.desc ? 'descending' : 'ascending') : undefined} className="relative whitespace-nowrap border-b border-line px-3 py-2 text-left text-xs font-medium text-muted">
                      <button type="button" onClick={() => sortBy(c.name)} className="inline-flex items-center gap-1 hover:text-ink">
                        {c.name === pk && <KeyRound aria-label="primary key" className="h-3 w-3" />}
                        {c.name}
                        {sort?.col === c.name && <span aria-hidden>{sort.desc ? '↓' : '↑'}</span>}
                      </button>
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {q.data.rows.map((row, ri) => (
                  <tr key={ri} className={trCls}>
                    {row.map((v, ci) => (
                      <td key={ci} className="max-w-[320px] px-3 py-1.5 align-top">
                        <button type="button" onClick={() => setCell({ col: cols[ci]?.name ?? '', value: v })} className="block w-full truncate text-left font-mono text-xs" title={v ?? 'NULL'}>
                          {v === null ? <span className="italic text-muted">NULL</span> : v === '' ? <span className="text-muted">''</span> : v}
                        </button>
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="flex flex-wrap items-center justify-between gap-2 px-4 py-2 text-xs text-muted">
            <span>
              Page {page} · {q.data.rows.length} rows · {q.data.duration_ms} ms{q.data.cell_truncated ? ' · long values truncated' : ''}
            </span>
            <div className="flex gap-1">
              <Button size="sm" disabled={page <= 1} onClick={() => (keyset ? setCursors((c) => c.slice(0, -1)) : setOffset((o) => Math.max(0, o - limit)))}>
                Previous
              </Button>
              <Button
                size="sm"
                disabled={!q.data.truncated || (keyset ? !q.data.next : offset + limit > 10000)}
                onClick={() => {
                  const next = q.data?.next
                  if (keyset && next) setCursors((c) => [...c, next])
                  else setOffset((o) => o + limit)
                }}
              >
                Next
              </Button>
            </div>
          </div>
        </>
      )}
      <Drawer open={!!cell} title={cell ? `${table}.${cell.col}` : ''} onClose={() => setCell(null)}>
        {cell && (
          <div>
            <div className="mb-2 flex justify-end">{cell.value !== null && <CopyButton text={cell.value} />}</div>
            {cell.value === null ? (
              <p className="italic text-muted">NULL</p>
            ) : (
              <pre className="whitespace-pre-wrap break-words rounded-lg bg-surface-2 p-3 font-mono text-xs">
                {(() => {
                  try {
                    const v = JSON.parse(cell.value)
                    return typeof v === 'object' && v !== null ? JSON.stringify(v, null, 2) : cell.value
                  } catch {
                    return cell.value
                  }
                })()}
              </pre>
            )}
          </div>
        )}
      </Drawer>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Relation view: Data preview / Schema / Indexes / Triggers

type Detail = { columns: GridResult; constraints: GridResult; indexes: GridResult; triggers: GridResult; partitions: GridResult }
type RelTab = 'data' | 'schema' | 'indexes' | 'triggers'

const CONSTRAINT: Record<string, string> = { p: 'primary key', f: 'foreign key', u: 'unique', c: 'check', x: 'exclusion' }

function RelationView({ id, schema, table, policy, rel, compactView }: { id: number; schema: string; table: string; policy: WorkspacePolicy; rel?: Record<string, string | null>; compactView: boolean }) {
  const [tab, setTab] = useState<RelTab>(policy.data_enabled ? 'data' : 'schema')
  const detail = useCatalog<Detail>(id, true, 'detail', schema, table)
  const pk = useMemo(() => {
    const c = detail.data ? records(detail.data.constraints).find((r) => r.kind === 'p') : undefined
    const m = c?.definition?.match(/^PRIMARY KEY \(([^,)]+)\)$/)
    return m?.[1] ? m[1].replace(/"/g, '') : null
  }, [detail.data])
  const facts = rel
    ? [KIND[rel.kind ?? ''] ?? rel.kind, rel.estimated_rows !== null ? `about ${compact(Number(rel.estimated_rows))} rows` : null, rel.total_bytes !== null ? bytes(Number(rel.total_bytes)) : null, rel.last_autovacuum ? `autovacuumed ${ago(rel.last_autovacuum)}` : null].filter(Boolean).join(' · ')
    : ''
  return (
    <div className="min-w-0">
      <div className="px-4 pt-3">
        <h3 className="truncate font-mono text-sm font-semibold text-ink">
          {schema}.{table}
        </h3>
        {facts && <p className="text-xs text-muted">{facts}</p>}
      </div>
      <div className="px-4 pt-2">
        <Tabs
          label="Relation views"
          value={tab}
          onChange={setTab}
          items={[
            { value: 'data', label: 'Data preview' },
            { value: 'schema', label: 'Schema', count: detail.data?.columns.rows.length },
            { value: 'indexes', label: 'Indexes', count: detail.data?.indexes.rows.length },
            { value: 'triggers', label: 'Triggers', count: detail.data?.triggers?.rows.length },
          ]}
        />
      </div>
      <div className="pt-3">
        {tab === 'data' ? (
          policy.data_enabled ? (
            <DataGrid key={`${schema}.${table}`} id={id} schema={schema} table={table} pk={pk} compactView={compactView} />
          ) : (
            <div className="px-4 pb-4">
              <Notice>
                <Lock aria-hidden className="mr-1 inline h-3.5 w-3.5" />
                Row browsing is switched off for this instance. Enable “Browse table rows” in the Explorer access settings.
              </Notice>
            </div>
          )
        ) : (
          <div className="overflow-x-auto px-4 pb-4">
            <Load q={detail} label="Loading structure">
              {(d) => {
                if (tab === 'schema') {
                  const cols = records(d.columns)
                  const cons = records(d.constraints)
                  return (
                    <div className="space-y-4">
                      <table className={tableCls}>
                        <thead>
                          <tr>
                            <Th>Column</Th>
                            <Th>Type</Th>
                            <Th>Nullable</Th>
                            <Th>Default</Th>
                          </tr>
                        </thead>
                        <tbody>
                          {cols.map((c) => (
                            <tr key={c.column} className={trCls}>
                              <td className={`${tdCls} font-mono`}>
                                {c.column} {c.column === pk && <Badge tone="accent">primary key</Badge>}
                              </td>
                              <td className={`${tdCls} font-mono text-xs text-ink-2`}>{c.type}</td>
                              <td className={tdCls}>{c.nullable === 't' || c.nullable === 'true' ? 'yes' : 'no'}</td>
                              <td className={`${tdCls} font-mono text-xs text-ink-2`}>{c.default_expression ?? '—'}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                      {cons.length > 0 && (
                        <table className={tableCls}>
                          <thead>
                            <tr>
                              <Th>Constraint</Th>
                              <Th>Kind</Th>
                              <Th>Definition</Th>
                            </tr>
                          </thead>
                          <tbody>
                            {cons.map((c) => (
                              <tr key={c.name} className={trCls}>
                                <td className={`${tdCls} font-mono`}>{c.name}</td>
                                <td className={tdCls}>{CONSTRAINT[c.kind ?? ''] ?? c.kind}</td>
                                <td className={`${tdCls} font-mono text-xs text-ink-2`}>{c.definition}</td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      )}
                    </div>
                  )
                }
                if (tab === 'indexes') {
                  const r = records(d.indexes)
                  if (r.length === 0) return <p className="text-sm text-muted">No indexes.</p>
                  return (
                    <table className={tableCls}>
                      <thead>
                        <tr>
                          <Th>Index</Th>
                          <Th align="right">Size</Th>
                          <Th align="right">Scans</Th>
                          <Th>Definition</Th>
                        </tr>
                      </thead>
                      <tbody>
                        {r.map((c) => (
                          <tr key={c.name} className={trCls}>
                            <td className={`${tdCls} font-mono`}>{c.name}</td>
                            <td className={tdNum}>{bytes(c.bytes === null ? null : Number(c.bytes))}</td>
                            <td className={tdNum}>{c.idx_scan === null ? '—' : compact(Number(c.idx_scan))}</td>
                            <td className={`${tdCls} font-mono text-xs text-ink-2`}>{c.definition}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  )
                }
                const r = d.triggers ? records(d.triggers) : []
                if (r.length === 0) return <p className="text-sm text-muted">No triggers.</p>
                return (
                  <table className={tableCls}>
                    <thead>
                      <tr>
                        <Th>Trigger</Th>
                        <Th>Enabled</Th>
                        <Th>Definition</Th>
                      </tr>
                    </thead>
                    <tbody>
                      {r.map((c) => (
                        <tr key={c.name} className={trCls}>
                          <td className={`${tdCls} font-mono`}>{c.name}</td>
                          <td className={tdCls}>{c.enabled === 'false' || c.enabled === 'f' ? <Badge tone="warn">disabled</Badge> : <Badge tone="good">enabled</Badge>}</td>
                          <td className={`${tdCls} font-mono text-xs text-ink-2`}>{c.definition}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )
              }}
            </Load>
          </div>
        )}
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Object tree

export interface ExplorerSelection {
  schema: string
  /** Relation name, or `fn:<name>(<args>)` / `type:<name>` for other objects. */
  object: string
}

function TreeGroup({ label, icon, count, open, onToggle, children }: { label: string; icon: ReactNode; count?: number; open: boolean; onToggle: () => void; children: ReactNode }) {
  return (
    <li>
      <button type="button" aria-expanded={open} onClick={onToggle} className="flex w-full items-center gap-1.5 px-3 py-1.5 text-left text-sm font-medium text-ink hover:bg-surface-2">
        {open ? <ChevronDown aria-hidden className="h-3.5 w-3.5 text-muted" /> : <ChevronRight aria-hidden className="h-3.5 w-3.5 text-muted" />}
        {icon}
        <span className="flex-1">{label}</span>
        {count !== undefined && <span className="num text-xs text-muted">{count}</span>}
      </button>
      {open && <ul className="pb-1">{children}</ul>}
    </li>
  )
}

function TreeItem({ active, icon, label, detail, onClick }: { active: boolean; icon: ReactNode; label: string; detail?: string; onClick: () => void }) {
  return (
    <li>
      <button
        type="button"
        aria-current={active ? 'true' : undefined}
        onClick={onClick}
        className={cx('flex w-full items-center gap-2 py-1 pr-3 pl-8 text-left text-sm hover:bg-surface-2', active && 'bg-accent-soft text-accent')}
      >
        {icon}
        <span className="min-w-0 flex-1 truncate font-mono text-[12.5px]">{label}</span>
        {detail && <span className="num shrink-0 text-xs text-muted">{detail}</span>}
      </button>
    </li>
  )
}

const ic = 'h-3.5 w-3.5 shrink-0 text-muted'

function ObjectInfo({ title, items }: { title: string; items: [string, ReactNode][] }) {
  return (
    <div className="space-y-3 p-4">
      <h3 className="font-mono text-sm font-semibold text-ink">{title}</h3>
      <KeyValue items={items} />
    </div>
  )
}

/**
 * Database explorer: schema objects on the left (tables, views, functions,
 * types) and the selected relation's rows and structure on the right. All
 * reads go through the workspace with the instance's separate browsing role.
 */
export function ExplorerPanel({ id, selection, onSelect, compactView = false, toolbar }: {
  id: number
  selection: ExplorerSelection | null
  onSelect: (s: ExplorerSelection) => void
  compactView?: boolean
  /** Extra controls before the schema select (e.g. an instance select). */
  toolbar?: ReactNode
}) {
  const policy = usePolicy(id)
  const p = policy.data
  const canMeta = !!p?.configured && p.metadata_enabled
  const schemas = useCatalog(id, canMeta, 'schemas')
  const schemaList = schemas.data ? records(schemas.data).map((r) => r.schema ?? '') : []
  const schema = selection?.schema || (schemaList.includes('public') ? 'public' : (schemaList[0] ?? ''))
  const [open, setOpen] = useState({ tables: true, views: true, functions: false, types: false })
  const relations = useCatalog(id, canMeta && !!schema, 'relations', schema)
  const functions = useCatalog(id, canMeta && !!schema && open.functions, 'functions', schema)
  const types = useCatalog(id, canMeta && !!schema && open.types, 'types', schema)
  const [term, setTerm] = useState('')
  const t = term.trim().toLowerCase()
  const match = (s: string | null | undefined) => !t || (s ?? '').toLowerCase().includes(t)

  const rels = relations.data ? records(relations.data) : []
  const tables = rels.filter((r) => (r.kind === 'r' || r.kind === 'p' || r.kind === 'f') && match(r.relation))
  const views = rels.filter((r) => (r.kind === 'v' || r.kind === 'm') && match(r.relation))
  const fns = functions.data ? records(functions.data).filter((r) => match(r.name)) : []
  const tys = types.data ? records(types.data).filter((r) => match(r.name)) : []

  const obj = selection?.schema === schema ? selection.object : ''
  const pick = (object: string) => onSelect({ schema, object })
  const relSel = obj && !obj.startsWith('fn:') && !obj.startsWith('type:') ? obj : ''
  const fnSel = obj.startsWith('fn:') ? fns.find((f) => `fn:${f.name}(${f.arguments})` === obj) : undefined
  const tySel = obj.startsWith('type:') ? tys.find((x) => `type:${x.name}` === obj) : undefined

  if (!p) return <div className="px-5 pb-5">{policy.error ? <ErrorBox error={policy.error} title="Could not check access" /> : <Spinner label="Checking access" />}</div>
  if (!canMeta) {
    return (
      <div className="space-y-3 px-5 pb-5">
        {toolbar && <div className="flex flex-wrap gap-2">{toolbar}</div>}
        {compactView ? (
          <Empty title="Browsing is not set up for this instance">
            Structure and rows are read with a separate, read-only role.{' '}
            <Link to={`/explorer/${id}`} className="text-accent hover:underline">
              Set up access
            </Link>
          </Empty>
        ) : (
          <AccessSetup id={id} policy={p} />
        )}
      </div>
    )
  }

  return (
    <div>
      <div className="flex flex-wrap items-center gap-2 px-5 pb-3">
        {toolbar}
        <label className="sr-only" htmlFor={`schema-${id}`}>
          Schema
        </label>
        <select id={`schema-${id}`} className={`${selectCls} min-w-32`} value={schema} onChange={(e) => onSelect({ schema: e.target.value, object: '' })}>
          {schemaList.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
        <div className="relative min-w-40 flex-1">
          <Search aria-hidden className="pointer-events-none absolute top-2 left-2.5 h-4 w-4 text-muted" />
          <input aria-label="Search objects" placeholder="Search tables, views, functions…" className={`${inputCls} pl-8`} value={term} onChange={(e) => setTerm(e.target.value)} />
        </div>
      </div>
      <div className={cx('grid border-t border-line', compactView ? 'md:grid-cols-[200px_minmax(0,1fr)]' : 'md:grid-cols-[260px_minmax(0,1fr)]')}>
        <nav aria-label="Schema objects" className={cx('overflow-y-auto border-line py-1 md:border-r', compactView ? 'max-h-[380px]' : 'max-h-[70vh]')}>
          {schemas.error ? (
            <div className="p-3">
              <ErrorBox error={schemas.error} title="Could not list schemas" />
            </div>
          ) : relations.error ? (
            <div className="p-3">
              <ErrorBox error={relations.error} title="Could not list objects" />
            </div>
          ) : !schema ? (
            <p className="px-3 py-3 text-sm text-muted">{schemas.data ? 'No accessible schema.' : 'Listing schemas…'}</p>
          ) : (
            <ul>
              <TreeGroup label="Tables" icon={<Table2 aria-hidden className={ic} />} count={relations.data ? tables.length : undefined} open={open.tables} onToggle={() => setOpen((o) => ({ ...o, tables: !o.tables }))}>
                {!relations.data ? <li className="px-8 text-xs text-muted">Loading…</li> : tables.length === 0 ? <li className="px-8 py-1 text-xs text-muted">None</li> : tables.map((r) => <TreeItem key={r.relation} active={relSel === r.relation} icon={<Table2 aria-hidden className={ic} />} label={r.relation ?? ''} detail={r.estimated_rows === null ? undefined : compact(Number(r.estimated_rows))} onClick={() => pick(r.relation ?? '')} />)}
              </TreeGroup>
              <TreeGroup label="Views" icon={<Eye aria-hidden className={ic} />} count={relations.data ? views.length : undefined} open={open.views} onToggle={() => setOpen((o) => ({ ...o, views: !o.views }))}>
                {!relations.data ? <li className="px-8 text-xs text-muted">Loading…</li> : views.length === 0 ? <li className="px-8 py-1 text-xs text-muted">None</li> : views.map((r) => <TreeItem key={r.relation} active={relSel === r.relation} icon={<Eye aria-hidden className={ic} />} label={r.relation ?? ''} detail={r.kind === 'm' ? 'mat.' : undefined} onClick={() => pick(r.relation ?? '')} />)}
              </TreeGroup>
              <TreeGroup label="Functions" icon={<FunctionSquare aria-hidden className={ic} />} count={functions.data ? fns.length : undefined} open={open.functions} onToggle={() => setOpen((o) => ({ ...o, functions: !o.functions }))}>
                {functions.error ? <li className="px-8 py-1 text-xs text-crit">Could not list functions.</li> : !functions.data ? <li className="px-8 text-xs text-muted">Loading…</li> : fns.length === 0 ? <li className="px-8 py-1 text-xs text-muted">None</li> : fns.map((f) => <TreeItem key={`${f.name}(${f.arguments})`} active={obj === `fn:${f.name}(${f.arguments})`} icon={<Braces aria-hidden className={ic} />} label={`${f.name}(${f.arguments ?? ''})`} onClick={() => pick(`fn:${f.name}(${f.arguments})`)} />)}
              </TreeGroup>
              <TreeGroup label="Types" icon={<Shapes aria-hidden className={ic} />} count={types.data ? tys.length : undefined} open={open.types} onToggle={() => setOpen((o) => ({ ...o, types: !o.types }))}>
                {types.error ? <li className="px-8 py-1 text-xs text-crit">Could not list types.</li> : !types.data ? <li className="px-8 text-xs text-muted">Loading…</li> : tys.length === 0 ? <li className="px-8 py-1 text-xs text-muted">None</li> : tys.map((x) => <TreeItem key={x.name} active={obj === `type:${x.name}`} icon={<Shapes aria-hidden className={ic} />} label={x.name ?? ''} detail={x.kind ?? undefined} onClick={() => pick(`type:${x.name}`)} />)}
              </TreeGroup>
            </ul>
          )}
        </nav>
        <div className="min-w-0 border-t border-line md:border-t-0">
          {relSel ? (
            <RelationView key={`${schema}.${relSel}`} id={id} schema={schema} table={relSel} policy={p} rel={rels.find((r) => r.relation === relSel)} compactView={compactView} />
          ) : fnSel ? (
            <ObjectInfo title={`${schema}.${fnSel.name}(${fnSel.arguments ?? ''})`} items={[['Kind', fnSel.kind ?? '—'], ['Returns', <code key="r" className="font-mono text-xs">{fnSel.returns ?? '—'}</code>], ['Language', fnSel.language ?? '—']]} />
          ) : tySel ? (
            <ObjectInfo title={`${schema}.${tySel.name}`} items={[['Kind', tySel.kind ?? '—'], ['Type', <code key="d" className="font-mono text-xs">{tySel.definition ?? '—'}</code>]]} />
          ) : (
            <div className="p-5">
              <Empty title="Choose an object">Select a table or view to preview rows and see its columns, indexes, and triggers.</Empty>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
