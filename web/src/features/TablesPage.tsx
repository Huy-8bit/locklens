import { useTables } from '../api/hooks'
import { Card, DataState, Empty, Notice, PageHeader, Tag } from '../components/ui'
import { fmtAgo, fmtBytes, fmtNumber, fmtPercent, fmtTime } from '../lib/format'
import { useTargetId } from './TargetOverviewPage'

export function TablesPage() {
  const id = useTargetId()
  const q = useTables(id)
  return (
    <>
      <PageHeader title="Tables and vacuum" subtitle="Tuple counts are statistics estimates; a dead-tuple ratio is not measured bloat. LockLens never runs VACUUM." />
      <DataState query={q} loadingLabel="Loading table statistics">
        {(d) => {
          const h = d.tables
          if (!h) return <Empty title="Table statistics not collected yet">They are read on a slow interval (about every 5 minutes) to keep overhead low.</Empty>
          const freezeMax = Number(h.settings.autovacuum_freeze_max_age ?? 'NaN')
          return (
            <div className="space-y-4">
              <div className="flex flex-wrap gap-2 text-xs">
                <Tag>database {h.datname}</Tag>
                <Tag>collected {fmtTime(h.at)} ({fmtAgo(h.at)})</Tag>
                <Tag tone={h.size_source === 'estimate' ? 'warn' : 'muted'}>sizes: {h.size_source}</Tag>
                <Tag>autovacuum {h.settings.autovacuum ?? 'unknown'}</Tag>
              </div>
              {h.warnings.map((w, i) => (
                <Notice key={i}>{w}</Notice>
              ))}
              <Card title="Vacuum in progress">
                {!d.vacuum_progress || d.vacuum_progress.length === 0 ? (
                  <p className="text-sm text-muted">No VACUUM is running{d.vacuum_progress_at ? ` (checked ${fmtTime(d.vacuum_progress_at)})` : ''}.</p>
                ) : (
                  <ul className="space-y-1 text-sm">
                    {d.vacuum_progress.map((v) => (
                      <li key={v.pid}>
                        pid {v.pid} · {v.datname ?? 'unknown database'} · {v.relation ?? 'hidden relation'} · phase {v.phase ?? 'hidden'} ·{' '}
                        {v.heap_blks_total ? fmtPercent((v.heap_blks_scanned ?? 0) / v.heap_blks_total) : 'unknown'} scanned
                      </li>
                    ))}
                  </ul>
                )}
                {d.long_transactions && d.long_transactions.length > 0 && (
                  <p className="mt-2 text-sm text-warn">{d.long_transactions.length} long-running transaction(s) are open; they can keep VACUUM from removing dead rows.</p>
                )}
              </Card>
              <Card title="Transaction ID age by database">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="text-left text-xs text-muted">
                      <th className="font-medium">Database</th>
                      <th className="text-right font-medium">XID age</th>
                      <th className="text-right font-medium">Of autovacuum_freeze_max_age</th>
                      <th className="text-right font-medium">Size</th>
                    </tr>
                  </thead>
                  <tbody className="tabular">
                    {h.databases.map((db) => (
                      <tr key={db.datname} className="border-t border-border">
                        <td>{db.datname}</td>
                        <td className="text-right">{fmtNumber(db.xid_age)}</td>
                        <td className="text-right">{db.xid_age !== null && Number.isFinite(freezeMax) ? fmtPercent(db.xid_age / freezeMax) : 'unknown'}</td>
                        <td className="text-right">{fmtBytes(db.size_bytes, 'no access')}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </Card>
              <Card title={`Tables (largest ${h.tables.length})`}>
                {h.tables.length === 0 ? (
                  <p className="text-sm text-muted">No user tables in {h.datname}.</p>
                ) : (
                  <div className="overflow-x-auto">
                    <table className="w-full min-w-[900px] text-sm">
                      <thead>
                        <tr className="border-b border-border text-left text-xs text-muted">
                          <th className="py-1.5 pr-3 font-medium">Table</th>
                          <th className="py-1.5 pr-3 text-right font-medium">Live (est.)</th>
                          <th className="py-1.5 pr-3 text-right font-medium">Dead (est.)</th>
                          <th className="py-1.5 pr-3 text-right font-medium">Dead ratio (est.)</th>
                          <th className="py-1.5 pr-3 font-medium">Last vacuum / autovacuum</th>
                          <th className="py-1.5 pr-3 font-medium">Last analyze / autoanalyze</th>
                          <th className="py-1.5 pr-3 text-right font-medium">Total size</th>
                          <th className="py-1.5 text-right font-medium">Frozen XID age</th>
                        </tr>
                      </thead>
                      <tbody className="tabular">
                        {h.tables.map((t) => {
                          const live = t.n_live_tup ?? null
                          const dead = t.n_dead_tup ?? null
                          const ratio = live !== null && dead !== null && live + dead > 0 ? dead / (live + dead) : null
                          return (
                            <tr key={t.relid} className="border-b border-border">
                              <td className="py-1.5 pr-3">
                                {t.schema}.{t.name}
                              </td>
                              <td className="py-1.5 pr-3 text-right">{fmtNumber(live)}</td>
                              <td className="py-1.5 pr-3 text-right">{fmtNumber(dead)}</td>
                              <td className={`py-1.5 pr-3 text-right ${ratio !== null && ratio >= 0.2 ? 'text-warn' : ''}`}>{fmtPercent(ratio)}</td>
                              <td className="py-1.5 pr-3">{fmtTime(t.last_vacuum, {}, 'never')} / {fmtTime(t.last_autovacuum, {}, 'never')}</td>
                              <td className="py-1.5 pr-3">{fmtTime(t.last_analyze, {}, 'never')} / {fmtTime(t.last_autoanalyze, {}, 'never')}</td>
                              <td className="py-1.5 pr-3 text-right">{fmtBytes(t.total_bytes ?? t.table_bytes)}</td>
                              <td className="py-1.5 text-right">{fmtNumber(t.frozenxid_age)}</td>
                            </tr>
                          )
                        })}
                      </tbody>
                    </table>
                  </div>
                )}
              </Card>
              <Card title="Indexes">
                <p className="mb-2 text-xs text-muted">Scan counts cover the time since statistics were last reset and do not reflect constraint enforcement. A low count alone is not a reason to drop an index; unique, primary-key, and constraint indexes are marked.</p>
                <div className="overflow-x-auto">
                  <table className="w-full min-w-[700px] text-sm">
                    <thead>
                      <tr className="border-b border-border text-left text-xs text-muted">
                        <th className="py-1.5 pr-3 font-medium">Index</th>
                        <th className="py-1.5 pr-3 font-medium">Table</th>
                        <th className="py-1.5 pr-3 text-right font-medium">Scans</th>
                        <th className="py-1.5 pr-3 text-right font-medium">Size</th>
                        <th className="py-1.5 font-medium">Kind</th>
                      </tr>
                    </thead>
                    <tbody className="tabular">
                      {h.indexes.map((i) => (
                        <tr key={i.indexrelid} className="border-b border-border">
                          <td className="py-1.5 pr-3">{i.name}</td>
                          <td className="py-1.5 pr-3">
                            {i.schema}.{i.table}
                          </td>
                          <td className="py-1.5 pr-3 text-right">{fmtNumber(i.idx_scan)}</td>
                          <td className="py-1.5 pr-3 text-right">{fmtBytes(i.index_bytes)}</td>
                          <td className="py-1.5">
                            <span className="flex flex-wrap gap-1">
                              {i.is_primary && <Tag>primary key</Tag>}
                              {i.is_unique && !i.is_primary && <Tag>unique</Tag>}
                              {i.is_constraint && !i.is_primary && <Tag>constraint</Tag>}
                            </span>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </Card>
            </div>
          )
        }}
      </DataState>
    </>
  )
}
