import { Link } from 'react-router'
import { useFleetLive } from '../api/hooks'
import { PageHeader } from '../components/Shell'
import { tableCls, tdCls, tdNum, Th, trCls } from '../components/Table'
import { Badge, Card, Empty, Load, Notice, StateBadge } from '../components/ui'
import { lagHealth, lagText, TopologyGraph } from '../components/widgets/TopologyGraph'
import { topology } from '../lib/fleet'
import { ago, bytes, seconds } from '../lib/format'
import { useScope } from '../lib/scope'

const TONE = { healthy: 'good', warning: 'warn', critical: 'crit', unknown: 'muted' } as const

export default function TopologyPage() {
  const { env } = useScope()
  const live = useFleetLive(env)
  return (
    <>
      <PageHeader title="Topology" subtitle="Primaries, the standbys streaming from them, and replication slots, as observed from pg_stat_replication and pg_stat_wal_receiver. LockLens only observes; it never promotes, fails over, or drops slots." />
      <Load q={live} label="Loading topology">
        {(d) => {
          const { groups, standalone } = topology(d.targets)
          const slots = groups.flatMap((g) => (g.primary.replication?.slots ?? []).map((s) => ({ p: g.primary, s })))
          const warnings = d.targets.flatMap((t) => (t.replication?.warnings ?? []).map((w) => `${t.target.name}: ${w}`))
          return (
            <div className="space-y-4">
              {warnings.map((w) => (
                <Notice key={w} tone="warn">
                  {w}
                </Notice>
              ))}
              <Card title="Replication graph" subtitle="A monitored standby is linked to the primary its WAL receiver streams from (matched by host)">
                <TopologyGraph targets={d.targets} />
              </Card>
              {groups.some((g) => g.edges.length > 0) && (
                <Card pad={false} title="Streaming standbys">
                  <div className="overflow-x-auto">
                    <table className={tableCls}>
                      <thead>
                        <tr>
                          <Th className="pl-5">Primary</Th>
                          <Th>Standby</Th>
                          <Th>State</Th>
                          <Th align="right">Not yet sent</Th>
                          <Th align="right">Not yet replayed</Th>
                          <Th align="right">Replay lag (time)</Th>
                          <Th>Health</Th>
                          <Th className="pr-5">Last reply</Th>
                        </tr>
                      </thead>
                      <tbody>
                        {groups.flatMap((g) =>
                          (g.primary.replication?.replicas ?? []).map((r, i) => {
                            const e = g.edges[i]
                            const h = e ? lagHealth(e) : 'unknown'
                            return (
                              <tr key={`${g.primary.target.id}-${r.pid}`} className={trCls}>
                                <td className={`${tdCls} pl-5`}>
                                  <Link to={`/databases/${g.primary.target.id}/replication`} className="font-medium hover:underline">
                                    {g.primary.target.name}
                                  </Link>
                                </td>
                                <td className={tdCls}>
                                  {e?.standby ? (
                                    <Link to={`/databases/${e.standby.target.id}/replication`} className="font-medium hover:underline">
                                      {e.standby.target.name}
                                    </Link>
                                  ) : (
                                    <span className="font-medium">{r.application_name || `pid ${r.pid}`}</span>
                                  )}
                                  <div className="text-xs text-muted">
                                    {r.client_addr ?? 'local'}
                                    {r.sync_state ? ` · ${r.sync_state}` : ''}
                                  </div>
                                </td>
                                <td className={tdCls}>
                                  <Badge tone={r.state === 'streaming' ? 'good' : 'warn'}>{r.state ?? 'unknown'}</Badge>
                                </td>
                                <td className={tdNum}>{bytes(r.sent_lag_bytes)}</td>
                                <td className={tdNum}>{bytes(r.replay_lag_bytes)}</td>
                                <td className={tdNum}>{r.replay_lag_seconds === null ? <span className="text-xs text-muted">idle primary</span> : seconds(r.replay_lag_seconds)}</td>
                                <td className={tdCls}>
                                  <Badge tone={TONE[h]}>
                                    {h} · {e ? lagText(e) : '—'}
                                  </Badge>
                                </td>
                                <td className={`${tdCls} pr-5 text-xs text-muted`}>{ago(r.reply_time)}</td>
                              </tr>
                            )
                          }),
                        )}
                      </tbody>
                    </table>
                  </div>
                  <p className="px-5 py-3 text-xs text-muted">Health: time lag under 1 s is healthy and under 30 s a warning; without a time lag, under 16 MiB not replayed is healthy and under 256 MiB a warning.</p>
                </Card>
              )}
              {slots.length > 0 && (
                <Card pad={false} title="Replication slots" subtitle="A slot keeps WAL until its consumer confirms it; an inactive slot can fill the disk">
                  <div className="overflow-x-auto">
                    <table className={tableCls}>
                      <thead>
                        <tr>
                          <Th className="pl-5">Primary</Th>
                          <Th>Slot</Th>
                          <Th>Type</Th>
                          <Th>Status</Th>
                          <Th align="right">WAL retained</Th>
                          <Th className="pr-5">WAL status</Th>
                        </tr>
                      </thead>
                      <tbody>
                        {slots.map(({ p, s }) => (
                          <tr key={`${p.target.id}-${s.slot_name}`} className={trCls}>
                            <td className={`${tdCls} pl-5`}>{p.target.name}</td>
                            <td className={`${tdCls} font-mono`}>{s.slot_name}</td>
                            <td className={tdCls}>{s.slot_type}</td>
                            <td className={tdCls}>{s.active ? <Badge tone="good">active</Badge> : <Badge tone="warn">inactive {s.inactive_since ? ago(s.inactive_since) : ''}</Badge>}</td>
                            <td className={tdNum}>{bytes(s.retained_bytes)}</td>
                            <td className={`${tdCls} pr-5`}>{s.wal_status ?? '—'}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </Card>
              )}
              {standalone.length > 0 && (
                <Card pad={false} title="Other instances" subtitle="Standalone servers, or standbys whose primary is not monitored here">
                  <ul className="divide-y divide-line">
                    {standalone.map((t) => (
                      <li key={t.target.id} className="flex flex-wrap items-center justify-between gap-2 px-5 py-2.5 text-sm">
                        <Link to={`/databases/${t.target.id}`} className="font-medium hover:underline">
                          {t.target.name}
                        </Link>
                        <span className="text-xs text-muted">
                          {t.replication?.role === 'standby' ? `standby of ${t.replication.standby?.sender_host ?? 'an unknown host'} · ${t.replication.standby?.wal_receiver_status ?? 'receiver not running'}` : t.replication ? 'no standby' : 'replication not collected yet'}
                        </span>
                        <StateBadge state={t.target.status.state} />
                      </li>
                    ))}
                  </ul>
                </Card>
              )}
              {d.targets.length === 0 && <Empty title="No enabled instance in this environment" />}
            </div>
          )
        }}
      </Load>
    </>
  )
}
