import { useMemo, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { ChevronRight, Database, Eye, KeyRound, Lock, RefreshCw, Search, Table2 } from 'lucide-react'
import { useQueryClient } from '@tanstack/react-query'
import { useCatalog, usePolicy, useSavePolicy, useTableData, useTables, type DataRequest } from '../api/hooks'
import type { GridResult, TableStat, WorkspacePolicy } from '../api/types'
import { PageHeader, useInstanceId } from '../components/Shell'
import { CopyButton } from '../components/SqlText'
import { tableCls, tdCls, tdNum, Th, trCls } from '../components/Table'
import { Badge, Button, Card, Confirm, cx, Drawer, Empty, ErrorBox, Field, inputCls, Load, Notice, Spinner, Tabs } from '../components/ui'
import { ago, bytes, compact, num } from '../lib/format'

/** Rows of a bounded result as objects keyed by column name. */
function records(r: GridResult): Record<string, string | null>[] {
  return r.rows.map((row) => Object.fromEntries(r.columns.map((c, i) => [c.name, row[i] ?? null])))
}

const KIND: Record<string, string> = { r: 'table', p: 'partitioned table', v: 'view', m: 'materialized view', f: 'foreign table' }

// ---------------------------------------------------------------------------
// Access setup

function AccessSetup({ id, policy }: { id: number; policy: WorkspacePolicy }) {
  const save = useSavePolicy(id)
  const [dsn, setDsn] = useState('')
  const [metadata, setMetadata] = useState(policy.metadata_enabled || !policy.configured)
  const [data, setData] = useState(policy.data_enabled)
  const [confirm, setConfirm] = useState(false)
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (data && !policy.data_enabled) setConfirm(true)
    else save.mutate({ metadata_enabled: metadata, data_enabled: data, dsn: dsn || undefined })
  }
  return (
    <Card title="Set up data access" subtitle="Browsing tables uses a separate PostgreSQL role, not the monitoring role.">
      <form onSubmit={submit} className="max-w-2xl space-y-3">
        <Notice>
          Create a login role with <code>USAGE</code> on the schemas and <code>SELECT</code> on the tables you want to browse, and nothing else (no superuser, no write privileges). LockLens connects with it only when you open the explorer, in read-only transactions with short timeouts, and never runs <code>COUNT(*)</code> or writes.
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
            <span className="block text-xs text-muted">Schemas, tables, columns, indexes, constraints, sizes.</span>
          </span>
        </label>
        <label className="flex items-start gap-2 text-sm">
          <input type="checkbox" className="mt-1" checked={data} onChange={(e) => setData(e.target.checked)} />
          <span>
            <span className="font-medium">Browse table rows</span>
            <span className="block text-xs text-muted">Read actual data, at most 100 rows per page. Rows can contain personal data.</span>
          </span>
        </label>
        <Button type="submit" variant="primary" busy={save.isPending}>
          Save access
        </Button>
      </form>
      <Confirm
        open={confirm}
        title="Allow browsing table rows?"
        confirmLabel="Allow"
        busy={save.isPending}
        onCancel={() => setConfirm(false)}
        onConfirm={() => {
          setConfirm(false)
          save.mutate({ metadata_enabled: metadata, data_enabled: data, dsn: dsn || undefined })
        }}
      >
        Anyone signed in to LockLens can then read rows of every table the browsing role can SELECT. Each access is audited (without the data itself).
      </Confirm>
    </Card>
  )
}

// ---------------------------------------------------------------------------
// Overview from monitoring statistics (works without browsing access)

function StatsOverview({ id }: { id: number }) {
  const q = useTables(id)
  return (
    <Load q={q} label="Loading table statistics">
      {(d) => {
        const h = d.tables
        if (!h) return <Empty title="No table statistics yet">They are collected every 5 minutes.</Empty>
        return (
          <div className="space-y-3">
            <Card pad={false} title="Databases on this server">
              <div className="overflow-x-auto">
                <table className={tableCls}>
                  <thead>
                    <tr>
                      <Th>Database</Th>
                      <Th align="right">Size</Th>
                      <Th align="right">Transaction ID age</Th>
                    </tr>
                  </thead>
                  <tbody>
                    {h.databases.map((db) => (
                      <tr key={db.datname} className={trCls}>
                        <td className={tdCls}>
                          {db.datname} {db.datname === h.datname && <Badge tone="accent">connected</Badge>}
                        </td>
                        <td className={tdNum}>{bytes(db.size_bytes)}</td>
                        <td className={tdNum}>{compact(db.xid_age)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </Card>
            <TableStatsList stats={h.tables} />
          </div>
        )
      }}
    </Load>
  )
}

function TableStatsList({ stats }: { stats: TableStat[] }) {
  const [term, setTerm] = useState('')
  const rows = stats.filter((s) => !term || `${s.schema}.${s.name}`.toLowerCase().includes(term.toLowerCase()))
  return (
    <Card pad={false} title="Largest tables" subtitle="From PostgreSQL statistics (estimates), refreshed every 5 minutes" actions={<input aria-label="Filter tables" placeholder="Filter…" className={`${inputCls} w-48`} value={term} onChange={(e) => setTerm(e.target.value)} />}>
      <div className="overflow-x-auto">
        <table className={tableCls}>
          <thead>
            <tr>
              <Th>Table</Th>
              <Th align="right">Live rows</Th>
              <Th align="right">Dead rows</Th>
              <Th align="right">Table</Th>
              <Th align="right">Indexes</Th>
              <Th align="right">Total</Th>
              <Th>Last vacuum</Th>
            </tr>
          </thead>
          <tbody>
            {rows.slice(0, 100).map((s) => (
              <tr key={s.relid} className={trCls}>
                <td className={tdCls}>
                  <span className="font-mono">
                    {s.schema}.{s.name}
                  </span>
                </td>
                <td className={tdNum}>{compact(s.n_live_tup)}</td>
                <td className={tdNum}>{compact(s.n_dead_tup)}</td>
                <td className={tdNum}>{bytes(s.table_bytes)}</td>
                <td className={tdNum}>{bytes(s.indexes_bytes)}</td>
                <td className={tdNum}>{bytes(s.total_bytes)}</td>
                <td className={`${tdCls} text-xs text-muted`}>{ago(s.last_autovacuum ?? s.last_vacuum)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  )
}

// ---------------------------------------------------------------------------
// Table data grid

function DataGrid({ id, schema, table, pk }: { id: number; schema: string; table: string; pk: string | null }) {
  const [sort, setSort] = useState<{ col: string; desc: boolean } | null>(null)
  const [filter, setFilter] = useState<{ col: string; op: string; value: string } | null>(null)
  const [draft, setDraft] = useState({ col: '', op: 'contains', value: '' })
  const [limit, setLimit] = useState(25)
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
                    <th key={c.name} scope="col" aria-sort={sort?.col === c.name ? (sort.desc ? 'descending' : 'ascending') : undefined} className="whitespace-nowrap border-b border-line px-3 py-2 text-left text-xs font-medium text-muted">
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
              <Button
                size="sm"
                disabled={page <= 1}
                onClick={() => (keyset ? setCursors((c) => c.slice(0, -1)) : setOffset((o) => Math.max(0, o - limit)))}
              >
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
              <pre className="whitespace-pre-wrap break-words rounded-lg bg-surface-2 p-3 font-mono text-xs">{(() => {
                try {
                  const v = JSON.parse(cell.value)
                  return typeof v === 'object' && v !== null ? JSON.stringify(v, null, 2) : cell.value
                } catch {
                  return cell.value
                }
              })()}</pre>
            )}
          </div>
        )}
      </Drawer>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Table view

type Detail = { columns: GridResult; constraints: GridResult; indexes: GridResult; partitions: GridResult }

function TableView({ id, schema, table, policy, stat }: { id: number; schema: string; table: string; policy: WorkspacePolicy; stat?: TableStat }) {
  const [tab, setTab] = useState<'data' | 'columns' | 'indexes' | 'constraints'>(policy.data_enabled ? 'data' : 'columns')
  const detail = useCatalog<Detail>(id, true, 'detail', schema, table)
  const pk = useMemo(() => {
    const c = detail.data ? records(detail.data.constraints).find((r) => r.kind === 'p') : undefined
    const m = c?.definition?.match(/^PRIMARY KEY \(([^,)]+)\)$/)
    return m?.[1] ? m[1].replace(/"/g, '') : null
  }, [detail.data])
  return (
    <Card pad={false} title={<span className="font-mono">{schema}.{table}</span>} subtitle={stat ? `about ${compact(stat.n_live_tup)} rows · ${bytes(stat.total_bytes)} on disk · vacuumed ${ago(stat.last_autovacuum ?? stat.last_vacuum)}` : undefined}>
      <div className="px-4">
        <Tabs
          label="Table views"
          value={tab}
          onChange={setTab}
          items={[
            { value: 'data', label: 'Data' },
            { value: 'columns', label: 'Columns', count: detail.data?.columns.rows.length },
            { value: 'indexes', label: 'Indexes', count: detail.data?.indexes.rows.length },
            { value: 'constraints', label: 'Constraints', count: detail.data?.constraints.rows.length },
          ]}
        />
      </div>
      <div className="pt-3">
        {tab === 'data' ? (
          policy.data_enabled ? (
            <DataGrid key={`${schema}.${table}`} id={id} schema={schema} table={table} pk={pk} />
          ) : (
            <div className="px-4 pb-4">
              <Notice>
                <Lock aria-hidden className="mr-1 inline h-3.5 w-3.5" />
                Row browsing is switched off for this instance. Enable “Browse table rows” in the access settings below.
              </Notice>
            </div>
          )
        ) : (
          <div className="px-4 pb-4">
            <Load q={detail} label="Loading structure">
              {(d) => {
                const r = records(d[tab])
                if (r.length === 0) return <p className="text-sm text-muted">None.</p>
                if (tab === 'columns')
                  return (
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
                        {r.map((c) => (
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
                  )
                if (tab === 'indexes')
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
                return (
                  <table className={tableCls}>
                    <thead>
                      <tr>
                        <Th>Constraint</Th>
                        <Th>Kind</Th>
                        <Th>Definition</Th>
                      </tr>
                    </thead>
                    <tbody>
                      {r.map((c) => (
                        <tr key={c.name} className={trCls}>
                          <td className={`${tdCls} font-mono`}>{c.name}</td>
                          <td className={tdCls}>{({ p: 'primary key', f: 'foreign key', u: 'unique', c: 'check', x: 'exclusion' } as Record<string, string>)[c.kind ?? ''] ?? c.kind}</td>
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
    </Card>
  )
}

// ---------------------------------------------------------------------------

export default function ExplorerPage() {
  const id = useInstanceId()
  const { schema = '', table = '' } = useParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const policy = usePolicy(id)
  const tables = useTables(id)
  const p = policy.data
  const canMeta = !!p?.configured && p.metadata_enabled
  const schemas = useCatalog(id, canMeta, 'schemas')
  const relations = useCatalog(id, canMeta && !!schema, 'relations', schema)
  const [term, setTerm] = useState('')
  const [showAccess, setShowAccess] = useState(false)
  const schemaList = schemas.data ? records(schemas.data).map((r) => r.schema ?? '') : []
  const activeSchema = schema || (schemaList.includes('public') ? 'public' : schemaList[0] ?? '')
  const stat = tables.data?.tables?.tables.find((s) => s.schema === schema && s.name === table)

  return (
    <>
      <PageHeader
        title="Data explorer"
        subtitle="Databases, schemas, tables, and their rows."
        actions={
          canMeta && (
            <>
              <Button size="sm" onClick={() => void qc.invalidateQueries({ queryKey: ['t', id, 'catalog'] })}>
                <RefreshCw aria-hidden className="h-3.5 w-3.5" /> Refresh structure
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setShowAccess((s) => !s)}>
                <KeyRound aria-hidden className="h-3.5 w-3.5" /> Access settings
              </Button>
            </>
          )
        }
      />
      <Load q={policy} label="Checking access">
        {(pol) =>
          !canMeta ? (
            <div className="space-y-3">
              <AccessSetup id={id} policy={pol} />
              <StatsOverview id={id} />
            </div>
          ) : (
            <div className="space-y-3">
              {showAccess && <AccessSetup id={id} policy={pol} />}
              <div className="grid gap-3 lg:grid-cols-[280px_minmax(0,1fr)]">
                <Card pad={false} title={<span className="inline-flex items-center gap-1.5"><Database aria-hidden className="h-3.5 w-3.5" /> Tables</span>}>
                  <div className="px-3 pb-2">
                    <label className="text-xs text-muted">
                      Schema
                      <select
                        className={`${inputCls} mt-0.5`}
                        value={activeSchema}
                        onChange={(e) => navigate(`/i/${id}/explorer/${encodeURIComponent(e.target.value)}`)}
                      >
                        {schemaList.map((s) => (
                          <option key={s} value={s}>
                            {s}
                          </option>
                        ))}
                      </select>
                    </label>
                    <input aria-label="Filter tables" placeholder="Filter tables…" className={`${inputCls} mt-2`} value={term} onChange={(e) => setTerm(e.target.value)} />
                  </div>
                  <div className="max-h-[65vh] overflow-y-auto border-t border-line">
                    {!schema ? (
                      activeSchema ? (
                        <Link to={`/i/${id}/explorer/${encodeURIComponent(activeSchema)}`} className="block px-3 py-3 text-sm text-accent hover:underline">
                          Open schema {activeSchema}
                        </Link>
                      ) : (
                        <p className="px-3 py-3 text-sm text-muted">{schemas.error ? 'Could not list schemas.' : 'No accessible schema.'}</p>
                      )
                    ) : relations.error ? (
                      <div className="p-3">
                        <ErrorBox error={relations.error} title="Could not list tables" />
                      </div>
                    ) : !relations.data ? (
                      <div className="px-3">
                        <Spinner label="Listing tables" />
                      </div>
                    ) : (
                      <ul>
                        {records(relations.data)
                          .filter((r) => !term || r.relation?.toLowerCase().includes(term.toLowerCase()))
                          .map((r) => (
                            <li key={r.relation}>
                              <Link
                                to={`/i/${id}/explorer/${encodeURIComponent(schema)}/${encodeURIComponent(r.relation ?? '')}`}
                                aria-current={r.relation === table ? 'page' : undefined}
                                className={cx('flex items-center gap-2 px-3 py-1.5 text-sm hover:bg-surface-2', r.relation === table && 'bg-accent-soft text-accent')}
                              >
                                {r.kind === 'v' || r.kind === 'm' ? <Eye aria-hidden className="h-3.5 w-3.5 shrink-0 text-muted" /> : <Table2 aria-hidden className="h-3.5 w-3.5 shrink-0 text-muted" />}
                                <span className="min-w-0 flex-1 truncate font-mono text-[13px]">{r.relation}</span>
                                <span className="num shrink-0 text-xs text-muted">{r.estimated_rows === null ? '' : compact(Number(r.estimated_rows))}</span>
                              </Link>
                            </li>
                          ))}
                      </ul>
                    )}
                  </div>
                </Card>
                <div className="min-w-0 space-y-3">
                  {table ? (
                    <TableView key={`${schema}.${table}`} id={id} schema={schema} table={table} policy={pol} stat={stat} />
                  ) : schema && relations.data ? (
                    <Card pad={false} title={<span className="inline-flex items-center gap-1 font-mono">{schema} <ChevronRight aria-hidden className="h-3.5 w-3.5" /></span>} subtitle="Choose a table to see its rows and structure">
                      <div className="overflow-x-auto">
                        <table className={tableCls}>
                          <thead>
                            <tr>
                              <Th>Name</Th>
                              <Th>Kind</Th>
                              <Th align="right">Rows (estimate)</Th>
                              <Th align="right">Size</Th>
                              <Th align="right">Dead rows</Th>
                              <Th>Last autovacuum</Th>
                            </tr>
                          </thead>
                          <tbody>
                            {records(relations.data).map((r) => (
                              <tr key={r.relation} className={trCls}>
                                <td className={tdCls}>
                                  <Link className="font-mono hover:underline" to={`/i/${id}/explorer/${encodeURIComponent(schema)}/${encodeURIComponent(r.relation ?? '')}`}>
                                    {r.relation}
                                  </Link>
                                </td>
                                <td className={`${tdCls} text-xs text-ink-2`}>{KIND[r.kind ?? ''] ?? r.kind}</td>
                                <td className={tdNum}>{r.estimated_rows === null ? '—' : compact(Number(r.estimated_rows))}</td>
                                <td className={tdNum}>{r.total_bytes === null ? '—' : bytes(Number(r.total_bytes))}</td>
                                <td className={tdNum}>{r.estimated_dead_tuples === null ? '—' : num(Number(r.estimated_dead_tuples))}</td>
                                <td className={`${tdCls} text-xs text-muted`}>{ago(r.last_autovacuum)}</td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    </Card>
                  ) : (
                    <StatsOverview id={id} />
                  )}
                </div>
              </div>
            </div>
          )
        }
      </Load>
    </>
  )
}

