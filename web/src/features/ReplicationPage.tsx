import { useReplication, useTarget } from '../api/hooks'
import { TimeRangePicker } from '../components/TimeRangePicker'
import { Card, DataState, Empty, KeyValue, Notice, PageHeader, Tag } from '../components/ui'
import { fmtBytes, fmtDuration, fmtNumber, fmtTime } from '../lib/format'
import { useTimeRange } from '../lib/time'
import { MetricChart, useTargetId } from './TargetOverviewPage'

export function ReplicationPage() {
  const id = useTargetId()
  const q = useReplication(id)
  const t = useTarget(id)
  const [range, setRange] = useTimeRange('1h')
  const standby = t.data?.status.is_standby
  return (
    <>
      <PageHeader title="Replication and WAL" subtitle="LockLens only observes replication; it never promotes, fails over, drops slots, or changes configuration." actions={<TimeRangePicker range={range} onChange={setRange} />} />
      <DataState query={q} loadingLabel="Loading replication state">
        {(d) => {
          const r = d.replication
          if (!r) return <Empty title="Replication state not collected yet" />
          return (
            <div className="space-y-4">
              <div className="flex flex-wrap gap-2 text-xs">
                <Tag>role: {r.role}</Tag>
                <Tag>observed {fmtTime(r.at)}</Tag>
                {r.current_lsn && <Tag>current LSN {r.current_lsn}</Tag>}
              </div>
              {r.warnings.map((w, i) => (
                <Notice key={i}>{w}</Notice>
              ))}
              {r.role === 'primary' && (
                <Card title={`Replicas (${r.replicas.length})`}>
                  {r.replicas.length === 0 ? (
                    <p className="text-sm text-muted">No replication connections.</p>
                  ) : (
                    <div className="overflow-x-auto">
                      <table className="w-full min-w-[860px] text-sm">
                        <thead>
                          <tr className="border-b border-border text-left text-xs text-muted">
                            <th className="py-1.5 pr-3 font-medium">Replica</th>
                            <th className="py-1.5 pr-3 font-medium">State</th>
                            <th className="py-1.5 pr-3 text-right font-medium">Sent lag</th>
                            <th className="py-1.5 pr-3 text-right font-medium">Flush lag</th>
                            <th className="py-1.5 pr-3 text-right font-medium">Replay lag (bytes)</th>
                            <th className="py-1.5 pr-3 text-right font-medium">replay_lag (time)</th>
                            <th className="py-1.5 font-medium">Replay LSN</th>
                          </tr>
                        </thead>
                        <tbody className="tabular">
                          {r.replicas.map((x) => (
                            <tr key={x.pid} className="border-b border-border">
                              <td className="py-1.5 pr-3">
                                {x.application_name || `pid ${x.pid}`} <span className="text-xs text-muted">{x.client_addr ?? ''}</span>
                              </td>
                              <td className="py-1.5 pr-3">
                                {x.state ?? 'hidden'} {x.sync_state && <span className="text-xs text-muted">({x.sync_state})</span>}
                              </td>
                              <td className="py-1.5 pr-3 text-right">{fmtBytes(x.sent_lag_bytes)}</td>
                              <td className="py-1.5 pr-3 text-right">{fmtBytes(x.flush_lag_bytes)}</td>
                              <td className="py-1.5 pr-3 text-right">{fmtBytes(x.replay_lag_bytes)}</td>
                              <td className="py-1.5 pr-3 text-right">{fmtDuration(x.replay_lag_seconds, 'null (idle or caught up)')}</td>
                              <td className="py-1.5 font-mono text-xs">{x.replay_lsn ?? 'hidden'}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  )}
                  <p className="mt-2 text-xs text-muted">Byte lag is the WAL distance from the primary's current position. The *_lag time columns are measured from standby feedback and are NULL when the standby is idle and caught up.</p>
                </Card>
              )}
              {r.standby && (
                <Card title="This server is a standby">
                  <KeyValue
                    items={[
                      ['WAL receiver', r.standby.wal_receiver_status ?? 'not running'],
                      ['Sender host', r.standby.sender_host ?? 'unknown'],
                      ['Slot', r.standby.slot_name ?? 'none'],
                      ['Received LSN', r.standby.receive_lsn ?? 'unknown'],
                      ['Replayed LSN', r.standby.replay_lsn ?? 'unknown'],
                      ['Received but not replayed', fmtBytes(r.standby.replay_delay_bytes)],
                      ['Replay', r.standby.replay_pause_state ?? 'unknown'],
                      ['Last replayed transaction committed at', fmtTime(r.standby.last_xact_replay_timestamp)],
                    ]}
                  />
                  <p className="mt-2 text-xs text-muted">The time since the last replayed transaction grows whenever the primary is idle, so it is shown as a timestamp and never as lag.</p>
                </Card>
              )}
              <Card title={`Replication slots (${r.slots.length})`}>
                {r.slots.length === 0 ? (
                  <p className="text-sm text-muted">No replication slots.</p>
                ) : (
                  <table className="w-full text-sm">
                    <thead>
                      <tr className="text-left text-xs text-muted">
                        <th className="font-medium">Slot</th>
                        <th className="font-medium">Type</th>
                        <th className="font-medium">Active</th>
                        <th className="font-medium">wal_status</th>
                        <th className="text-right font-medium">Retained WAL (estimate)</th>
                        <th className="text-right font-medium">safe_wal_size</th>
                      </tr>
                    </thead>
                    <tbody className="tabular">
                      {r.slots.map((s) => (
                        <tr key={s.slot_name} className="border-t border-border">
                          <td>{s.slot_name}</td>
                          <td>{s.slot_type}</td>
                          <td>{s.active ? 'yes' : <span className="text-warn">no</span>}</td>
                          <td>{s.wal_status ?? 'unknown'}</td>
                          <td className="text-right">{fmtBytes(s.retained_bytes)}</td>
                          <td className="text-right">{fmtBytes(s.safe_wal_size, 'unlimited')}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
              </Card>
              {r.archiver && (
                <Card title="WAL archiver">
                  <KeyValue
                    items={[
                      ['archive_mode', r.archiver.archive_mode],
                      ['Archived (since reset)', fmtNumber(r.archiver.archived_count)],
                      ['Failed (since reset)', fmtNumber(r.archiver.failed_count)],
                      ['Last archived', fmtTime(r.archiver.last_archived_time)],
                      ['Last failure', `${fmtTime(r.archiver.last_failed_time)} ${r.archiver.last_failed_wal ?? ''}`],
                    ]}
                  />
                </Card>
              )}
              <div className="grid gap-4 md:grid-cols-2">
                <MetricChart id={id} title={standby ? 'WAL received' : 'WAL generated'} metrics={[standby ? 'wal.received_bytes.per_s' : 'wal.bytes.per_s']} labels={['bytes/s']} format={(n) => `${fmtBytes(n)}/s`} range={range} height={180} />
                {standby ? (
                  <MetricChart id={id} title="Received but not replayed" metrics={['standby.replay_delay_bytes']} labels={['bytes']} format={(n) => fmtBytes(n)} range={range} height={180} />
                ) : (
                  <MetricChart id={id} title="Largest replica replay lag" metrics={['replication.max_replay_lag_bytes']} labels={['bytes']} format={(n) => fmtBytes(n)} range={range} height={180} />
                )}
                <MetricChart id={id} title="Largest retained WAL by a slot" metrics={['slots.max_retained_bytes']} labels={['bytes']} format={(n) => fmtBytes(n)} range={range} height={180} />
                <MetricChart id={id} title="Archive failures" metrics={['archiver.failed.delta']} labels={['failures']} format={(n) => fmtNumber(n)} range={range} height={180} />
              </div>
            </div>
          )
        }}
      </DataState>
    </>
  )
}
