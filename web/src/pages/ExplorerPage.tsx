import { useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router'
import { KeyRound, RefreshCw } from 'lucide-react'
import { useQueryClient } from '@tanstack/react-query'
import { useFleetLive, usePolicy, useTables } from '../api/hooks'
import type { TableStat } from '../api/types'
import { PageHeader } from '../components/Shell'
import { tableCls, tdCls, tdNum, Th, trCls } from '../components/Table'
import { Badge, Button, Card, Empty, inputCls, Load } from '../components/ui'
import { AccessSetup, ExplorerPanel } from '../components/widgets/ExplorerPanel'
import { InstanceSelect } from '../components/widgets/InstanceSelect'
import { ago, bytes, compact } from '../lib/format'
import { useScope } from '../lib/scope'

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
  const several = new Set(stats.map((s) => s.datname)).size > 1
  const rows = stats.filter((s) => !term || `${s.datname}.${s.schema}.${s.name}`.toLowerCase().includes(term.toLowerCase()))
  return (
    <Card pad={false} title="Largest tables" subtitle="From PostgreSQL statistics (estimates), refreshed every 5 minutes" actions={<input aria-label="Filter tables" placeholder="Filter…" className={`${inputCls} w-48`} value={term} onChange={(e) => setTerm(e.target.value)} />}>
      <div className="overflow-x-auto">
        <table className={tableCls}>
          <thead>
            <tr>
              {several && <Th>Database</Th>}
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
              <tr key={`${s.datname}.${s.relid}`} className={trCls}>
                {several && <td className={`${tdCls} text-xs`}>{s.datname}</td>}
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

function InstanceExplorer({ id, list }: { id: number; list: { id: number; name: string }[] }) {
  const { schema = '', object = '' } = useParams()
  const [params, setParams] = useSearchParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const policy = usePolicy(id)
  const p = policy.data
  const canMeta = !!p?.configured && p.metadata_enabled
  const showAccess = params.get('access') === '1'
  const toggleAccess = () =>
    setParams(
      (q) => {
        if (showAccess) q.delete('access')
        else q.set('access', '1')
        return q
      },
      { replace: true },
    )
  const go = (n: number, s = '', o = '') => navigate(['/explorer', n, s && encodeURIComponent(s), s && o && encodeURIComponent(o)].filter(Boolean).join('/'))

  return (
    <div className="space-y-4">
      {canMeta && showAccess && p && <AccessSetup id={id} policy={p} />}
      <Card
        pad={false}
        title="Database objects"
        actions={
          canMeta ? (
            <>
              <Button size="sm" onClick={() => void qc.invalidateQueries({ queryKey: ['t', id, 'catalog'] })}>
                <RefreshCw aria-hidden className="h-3.5 w-3.5" /> Refresh structure
              </Button>
              <Button size="sm" variant="ghost" aria-expanded={showAccess} onClick={toggleAccess}>
                <KeyRound aria-hidden className="h-3.5 w-3.5" /> Access settings
              </Button>
            </>
          ) : undefined
        }
      >
        <ExplorerPanel id={id} selection={{ schema, object }} onSelect={(sel) => go(id, sel.schema, sel.object)} toolbar={<InstanceSelect targets={list} value={id} onChange={(n) => go(n)} />} />
      </Card>
      {(!canMeta || !object) && p && <StatsOverview id={id} />}
    </div>
  )
}

export default function ExplorerPage() {
  const { env } = useScope()
  const live = useFleetLive(env)
  const wanted = Number(useParams().targetId ?? 0)
  return (
    <>
      <PageHeader title="Explorer" subtitle="Schemas, tables, views, functions, and types, with a preview of rows. Read with a separate, read-only role, never the monitoring role." />
      <Load q={live} label="Loading instances">
        {(d) => {
          const list = d.targets.map((t) => t.target)
          const id = list.some((t) => t.id === wanted) ? wanted : (list[0]?.id ?? 0)
          if (!id) return <Empty title="No enabled instance in this environment" />
          return <InstanceExplorer key={id} id={id} list={list} />
        }}
      </Load>
    </>
  )
}
