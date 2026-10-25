import { useReplication } from '../api/hooks'
import { MetricCard, slot } from '../components/MetricCard'
import { RangePicker } from '../components/RangePicker'
import { PageHeader, useInstanceId } from '../components/Shell'
import { tableCls, tdCls, tdNum, Th, trCls } from '../components/Table'
import { Badge, Card, Empty, KeyValue, Load, Notice, Stat } from '../components/ui'
import { ago, bytes, num, seconds, time } from '../lib/format'
import { useRange } from '../lib/time'

export default function ReplicationPage() {
  const id = useInstanceId()
  const [range, setRange] = useRange()
  const q = useReplication(id)
  return (
    <>
      <PageHeader title="Replication" subtitle="Standbys, replication slots, and WAL. LockLens only observes; it never promotes, fails over, or drops slots." actions={<RangePicker value={range} onChange={setRange} />} />
      <Load q={q} label="Loading replication state">
        {(d) => {
          const r = d.replication
          if (!r) return <Empty title="No replication data yet">Collected every 30 seconds once the instance is reachable.</Empty>
          const maxLag = Math.max(...r.replicas.map((x) => x.replay_lag_bytes ?? 0), 0)
          const maxSlot = Math.max(...r.slots.map((s) => s.retained_bytes ?? 0), 0)
          return (
            <div className="space-y-3">
              <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
                <Stat label="Role" value={r.role === 'primary' ? 'Primary' : 'Standby'} sub={`observed ${ago(r.at)}`} />
                {r.role === 'primary' ? (
                  <>
                    <Stat label="Connected standbys" value={num(r.replicas.length)} />
                    <Stat label="Largest replay lag" value={r.replicas.length ? bytes(maxLag) : '—'} tone={maxLag > 64 * 1024 * 1024 ? 'warn' : undefined} />
                    <Stat label="WAL kept by slots" value={r.slots.length ? bytes(maxSlot) : '—'} sub={`${num(r.slots.length)} slot(s)`} tone={maxSlot > 1024 ** 3 ? 'warn' : undefined} />
                  </>
                ) : (
                  <>
                    <Stat label="WAL receiver" value={r.standby?.wal_receiver_status ?? 'not running'} tone={r.standby?.wal_receiver_status === 'streaming' ? 'good' : 'warn'} />
                    <Stat label="Received, not replayed" value={bytes(r.standby?.replay_delay_bytes)} />
                    <Stat label="Replay" value={r.standby?.replay_pause_state === 'not paused' || !r.standby?.replay_pause_state ? 'running' : r.standby.replay_pause_state} />
                  </>
                )}
              </div>
              {r.warnings.map((w) => (
                <Notice key={w} tone="warn">
                  {w}
                </Notice>
              ))}
              {r.role === 'primary' ? (
                <Card pad={false} title="Standbys streaming from this server">
                  {r.replicas.length === 0 ? (
                    <p className="px-4 pb-4 text-sm text-muted">No standby is connected.</p>
                  ) : (
                    <div className="overflow-x-auto">
                      <table className={tableCls}>
                        <thead>
                          <tr>
                            <Th>Standby</Th>
                            <Th>State</Th>
                            <Th align="right">Not yet sent</Th>
                            <Th align="right">Not yet replayed</Th>
                            <Th align="right">Replay lag (time)</Th>
                            <Th>Last reply</Th>
                          </tr>
                        </thead>
                        <tbody>
                          {r.replicas.map((x) => (
                            <tr key={x.pid} className={trCls}>
                              <td className={tdCls}>
                                <div className="font-medium">{x.application_name || `pid ${x.pid}`}</div>
                                <div className="text-xs text-muted">{x.client_addr ?? 'local'}{x.sync_state ? ` · ${x.sync_state}` : ''}</div>
                              </td>
                              <td className={tdCls}>
                                <Badge tone={x.state === 'streaming' ? 'good' : 'warn'}>{x.state ?? 'unknown'}</Badge>
                              </td>
                              <td className={tdNum}>{bytes(x.sent_lag_bytes)}</td>
                              <td className={tdNum}>{bytes(x.replay_lag_bytes)}</td>
                              <td className={tdNum}>{x.replay_lag_seconds === null ? <span className="text-xs text-muted">idle primary</span> : seconds(x.replay_lag_seconds)}</td>
                              <td className={`${tdCls} text-xs text-muted`}>{ago(x.reply_time)}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  )}
                </Card>
              ) : (
                <Card title="This server is a standby">
                  <KeyValue
                    items={[
                      ['Streaming from', r.standby?.sender_host ?? '—'],
                      ['Slot', r.standby?.slot_name ?? '—'],
                      ['Received up to', r.standby?.receive_lsn ?? '—'],
                      ['Replayed up to', r.standby?.replay_lsn ?? '—'],
                      ['Last replayed transaction', time(r.standby?.last_xact_replay_timestamp, { date: true })],
                      ['Last message from primary', ago(r.standby?.last_msg_receipt_time)],
                    ]}
                  />
                  <p className="mt-3 text-xs text-muted">The time since the last replayed transaction grows while the primary is idle, so it is not shown as lag.</p>
                </Card>
              )}
              {r.slots.length > 0 && (
                <Card pad={false} title="Replication slots" subtitle="A slot keeps WAL until its consumer confirms it; an inactive slot can fill the disk">
                  <div className="overflow-x-auto">
                    <table className={tableCls}>
                      <thead>
                        <tr>
                          <Th>Slot</Th>
                          <Th>Type</Th>
                          <Th>Status</Th>
                          <Th align="right">WAL retained</Th>
                          <Th>WAL status</Th>
                        </tr>
                      </thead>
                      <tbody>
                        {r.slots.map((s) => (
                          <tr key={s.slot_name} className={trCls}>
                            <td className={`${tdCls} font-mono`}>{s.slot_name}</td>
                            <td className={tdCls}>{s.slot_type}</td>
                            <td className={tdCls}>{s.active ? <Badge tone="good">active</Badge> : <Badge tone="warn">inactive {s.inactive_since ? ago(s.inactive_since) : ''}</Badge>}</td>
                            <td className={tdNum}>{bytes(s.retained_bytes)}</td>
                            <td className={tdCls}>{s.wal_status ?? '—'}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </Card>
              )}
              <div className="grid gap-3 lg:grid-cols-2">
                {r.role === 'primary' ? (
                  <>
                    <MetricCard id={id} range={range} title="Largest replica lag" format={(n) => bytes(n)} height={180} defs={[{ metric: 'replication.max_replay_lag_bytes', label: 'Replay lag', color: slot(0) }]} />
                    <MetricCard id={id} range={range} title="WAL generated" format={(n) => `${bytes(n)}/s`} height={180} defs={[{ metric: 'wal.bytes.per_s', label: 'WAL', color: slot(0) }]} />
                  </>
                ) : (
                  <>
                    <MetricCard id={id} range={range} title="Received, not yet replayed" format={(n) => bytes(n)} height={180} defs={[{ metric: 'standby.replay_delay_bytes', label: 'Replay delay', color: slot(0) }]} />
                    <MetricCard id={id} range={range} title="WAL received" format={(n) => `${bytes(n)}/s`} height={180} defs={[{ metric: 'wal.received_bytes.per_s', label: 'WAL', color: slot(0) }]} />
                  </>
                )}
              </div>
              {r.archiver && (
                <Card title="WAL archiving">
                  <KeyValue
                    items={[
                      ['archive_mode', r.archiver.archive_mode],
                      ['Archived since reset', num(r.archiver.archived_count)],
                      ['Failed since reset', num(r.archiver.failed_count)],
                      ['Last archived', time(r.archiver.last_archived_time, { date: true })],
                      ['Last failure', r.archiver.last_failed_time ? `${time(r.archiver.last_failed_time, { date: true })} (${r.archiver.last_failed_wal})` : '—'],
                    ]}
                  />
                </Card>
              )}
            </div>
          )
        }}
      </Load>
    </>
  )
}
