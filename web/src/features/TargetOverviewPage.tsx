import { Link, useParams } from 'react-router'
import { useHost, useMetrics, useOverview } from '../api/hooks'
import type { MetricsResponse } from '../api/types'
import { FindingCard } from '../components/FindingCard'
import { MetricTile } from '../components/MetricTile'
import { TimeRangePicker } from '../components/TimeRangePicker'
import { TimeSeriesChart } from '../components/TimeSeriesChart'
import { Card, DataState, Empty, ErrorBox, KeyValue, Notice, PageHeader, SeverityBadge, Spinner, StateBadge, Tag } from '../components/ui'
import { fmtAgo, fmtBytes, fmtDuration, fmtNumber, fmtPercent, fmtRate, fmtTime } from '../lib/format'
import { useTimeRange } from '../lib/time'

/** "a / b", or a single "unknown" when neither value is known. */
function pair(a: number | null | undefined, b: number | null | undefined): string {
  if ((a === null || a === undefined) && (b === null || b === undefined)) return fmtNumber(a)
  return `${fmtNumber(a)} / ${fmtNumber(b)}`
}

export function useTargetId(): number {
  const { id } = useParams()
  return Number(id)
}

function seriesOf(d: MetricsResponse | undefined, metric: string, label = '') {
  return d?.series.find((s) => s.metric === metric && (s.label ?? '') === label)
}

export function MetricChart({ id, title, metrics, labels, format, range, height, integer }: {
  id: number
  title: string
  metrics: string[]
  labels: string[]
  format: (v: number) => string
  range: ReturnType<typeof useTimeRange>[0]
  height?: number
  integer?: boolean
}) {
  const q = useMetrics(id, metrics, range.params)
  return (
    <Card title={title}>
      {q.error && !q.data ? (
        <ErrorBox error={q.error} />
      ) : !q.data ? (
        <div style={{ height: height ?? 200 }}>
          <Spinner />
        </div>
      ) : (
        <TimeSeriesChart
          ariaLabel={title}
          series={metrics.map((m, i) => ({ key: `s${i}`, label: labels[i] ?? m, series: seriesOf(q.data, m) }))}
          stepSeconds={q.data.step_seconds}
          gaps={q.data.gaps}
          format={format}
          integer={integer}
          from={q.data.from}
          to={q.data.to}
          height={height}
        />
      )}
    </Card>
  )
}

function HostCard({ id }: { id: number }) {
  const q = useHost(id)
  return (
    <Card title="Host metrics (optional)">
      <DataState query={q}>
        {(h) =>
          !h.configured ? (
            <p className="text-sm text-muted">
              Unavailable: no node_exporter URL is configured for this target. Host metrics are optional; LockLens never infers CPU, memory, or disk state from SQL statistics.
            </p>
          ) : !h.available || !h.host ? (
            <Notice tone="warn">Unavailable: the node_exporter endpoint could not be read{h.error ? `: ${h.error}` : '.'}</Notice>
          ) : (
            <KeyValue
              items={[
                ['CPU utilisation', fmtPercent(h.host.cpu_utilization_ratio)],
                ['Memory available', `${fmtBytes(h.host.memory_available_bytes)} of ${fmtBytes(h.host.memory_total_bytes)}`],
                ['Disk read / write', `${fmtBytes(h.host.disk_read_bytes_per_second)}/s · ${fmtBytes(h.host.disk_write_bytes_per_second)}/s`],
                ['Network rx / tx', `${fmtBytes(h.host.network_receive_bytes_per_second)}/s · ${fmtBytes(h.host.network_transmit_bytes_per_second)}/s`],
                ...(h.host.filesystems ?? []).slice(0, 4).map((f): [string, string] => [`Disk ${f.mountpoint}`, `${fmtPercent(1 - f.avail_bytes / f.size_bytes)} used of ${fmtBytes(f.size_bytes)}`]),
                ['Observed', fmtTime(h.host.at)],
              ]}
            />
          )
        }
      </DataState>
    </Card>
  )
}

export function TargetOverviewPage() {
  const id = useTargetId()
  const q = useOverview(id)
  const [range, setRange] = useTimeRange('1h')
  return (
    <DataState query={q} loadingLabel="Loading target">
      {(d) => {
        const t = d.target
        const v = (k: string) => d.latest[k]?.value ?? null
        const caps = t.status.capabilities
        const current = d.findings.filter((f) => f.finding.status === 'active')
        const notEvaluated = d.findings.filter((f) => f.finding.status === 'insufficient_data')
        return (
          <>
            <PageHeader
              title={
                <span className="flex items-center gap-3">
                  {t.name} <StateBadge state={t.status.state} />
                </span>
              }
              subtitle={`${t.host}:${t.port}/${t.database} as ${t.user} · PostgreSQL ${t.status.server_version ?? 'unknown'}${t.status.is_standby ? ' (standby)' : ''} · last collected ${fmtAgo(t.status.last_success_at)}`}
              actions={<TimeRangePicker range={range} onChange={setRange} />}
            />
            {!d.connected && t.status.state !== 'disabled' && (
              <div className="mb-4">
                <Notice tone="crit">Not connected: {d.connect_error ?? t.status.last_error ?? 'no connection yet'}. Values below may be stale.</Notice>
              </div>
            )}
            {d.visibility_warnings && d.visibility_warnings.length > 0 && (
              <div className="mb-4 space-y-1">
                {d.visibility_warnings.map((w) => (
                  <Notice key={w.code} tone="warn">
                    Partial data: {w.message}
                  </Notice>
                ))}
              </div>
            )}
            <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
              <MetricTile label="Blocked sessions" value={fmtNumber(v('blocking.blocked_sessions'))} tone={(v('blocking.blocked_sessions') ?? 0) > 0 ? 'crit' : undefined} hint={`longest lock wait ${fmtDuration(v('blocking.max_lock_wait_seconds'), 'none')}`} />
              <MetricTile label="Active / idle in tx" value={pair(v('sessions.active'), v('sessions.idle_in_transaction'))} hint={`${fmtNumber(v('sessions.unknown_state'))} with unknown state`} />
              <MetricTile label="Connections" value={pair(v('connections.used'), v('connections.max'))} hint={`${fmtPercent(v('connections.usage_ratio'))} of available slots`} tone={(v('connections.usage_ratio') ?? 0) >= 0.8 ? 'warn' : undefined} />
              <MetricTile label="Oldest transaction" value={fmtDuration(v('transactions.oldest_age_seconds'), 'none open')} hint={`${fmtNumber(v('transactions.long'))} long-running`} />
              <MetricTile label="Commits / rollbacks" value={fmtRate(v('xact.commit.per_s'))} hint={`${fmtRate(v('xact.rollback.per_s'))} rollbacks`} />
              <MetricTile label="Cache hit ratio" value={fmtPercent(v('cache.hit_ratio'))} hint="shared buffers only" title="Share of block requests found in shared buffers during the last interval; reads served by the OS page cache count as misses." />
            </div>

            <div className="mt-4 grid gap-4 lg:grid-cols-3">
              <div className="space-y-4 lg:col-span-2">
                <MetricChart id={id} integer title="Sessions by state" metrics={['sessions.active', 'sessions.idle_in_transaction', 'sessions.waiting_lock', 'blocking.blocked_sessions']} labels={['active', 'idle in transaction', 'waiting for a lock', 'blocked']} format={(n) => fmtNumber(n, 1)} range={range} />
                <MetricChart id={id} title="Transactions per second" metrics={['xact.commit.per_s', 'xact.rollback.per_s']} labels={['commits/s', 'rollbacks/s']} format={(n) => fmtRate(n)} range={range} />
                <div className="grid gap-4 md:grid-cols-2">
                  <MetricChart id={id} title="Connection usage" metrics={['connections.usage_ratio']} labels={['used / available']} format={(n) => fmtPercent(n)} range={range} height={160} />
                  <MetricChart id={id} title="Oldest transaction age" metrics={['transactions.oldest_age_seconds']} labels={['oldest open transaction']} format={(n) => fmtDuration(n)} range={range} height={160} />
                  <MetricChart id={id} title="Temporary files written" metrics={['temp.bytes.per_s']} labels={['temp bytes/s']} format={(n) => `${fmtBytes(n)}/s`} range={range} height={160} />
                  <MetricChart id={id} title={t.status.is_standby ? 'WAL received' : 'WAL generated'} metrics={[t.status.is_standby ? 'wal.received_bytes.per_s' : 'wal.bytes.per_s']} labels={['WAL bytes/s']} format={(n) => `${fmtBytes(n)}/s`} range={range} height={160} />
                  <MetricChart id={id} integer title="Deadlocks (PostgreSQL detector)" metrics={['deadlocks.delta']} labels={['deadlocks']} format={(n) => fmtNumber(n)} range={range} height={160} />
                  <MetricChart id={id} integer title="Checkpoints" metrics={['checkpoints.timed.delta', 'checkpoints.requested.delta']} labels={['timed', 'requested']} format={(n) => fmtNumber(n)} range={range} height={160} />
                  {caps?.pg_stat_io && <MetricChart id={id} title="I/O operations (pg_stat_io)" metrics={['io.reads.per_s', 'io.writes.per_s']} labels={['reads/s', 'writes/s']} format={(n) => fmtRate(n)} range={range} height={160} />}
                  <MetricChart id={id} title="Total database size (slow interval)" metrics={['db.total_size_bytes']} labels={['size']} format={(n) => fmtBytes(n)} range={range} height={160} />
                </div>
              </div>
              <div className="space-y-4">
                <Card title="Findings" actions={<Link className="text-sm text-accent underline" to={`/targets/${id}/blocking`}>Blocking explorer</Link>}>
                  {current.length === 0 ? (
                    <p className="text-sm text-muted">No active findings. Rules that could not run are listed below; their absence is not a pass.</p>
                  ) : (
                    <ul className="space-y-2">
                      {current.map((f) => (
                        <FindingCard key={f.id} finding={f.finding} compact />
                      ))}
                    </ul>
                  )}
                  {notEvaluated.length > 0 && (
                    <details className="mt-3 text-sm">
                      <summary className="cursor-pointer text-muted">{notEvaluated.length} rule(s) not evaluated (insufficient data)</summary>
                      <ul className="mt-2 space-y-2">
                        {notEvaluated.map((f) => (
                          <FindingCard key={f.id} finding={f.finding} />
                        ))}
                      </ul>
                    </details>
                  )}
                </Card>
                <Card title={`Open incidents (${d.open_incident_count})`}>
                  {d.open_incidents.length === 0 ? (
                    <p className="text-sm text-muted">None.</p>
                  ) : (
                    <ul className="space-y-2 text-sm">
                      {d.open_incidents.map((i) => (
                        <li key={i.id}>
                          <Link to={`/incidents/${i.id}`} className="flex items-center gap-2 hover:underline">
                            <SeverityBadge severity={i.severity} /> <span className="truncate">{i.title}</span>
                          </Link>
                          <p className="text-xs text-muted">opened {fmtTime(i.opened_at)}</p>
                        </li>
                      ))}
                    </ul>
                  )}
                </Card>
                <Card title="Collectors and capabilities">
                  <ul className="space-y-1.5 text-sm">
                    {Object.values(t.status.collectors).map((c) => (
                      <li key={c.name} className="flex flex-wrap items-center justify-between gap-2">
                        <span className="font-medium">{c.name}</span>
                        {!c.enabled ? (
                          <Tag>unavailable</Tag>
                        ) : c.last_error ? (
                          <Tag tone="crit">failing</Tag>
                        ) : c.last_success ? (
                          <Tag tone="ok">every {fmtDuration(c.interval_seconds)}</Tag>
                        ) : (
                          <Tag>pending</Tag>
                        )}
                        {!c.enabled && c.disabled_reason && <p className="w-full text-xs text-muted">{c.disabled_reason}</p>}
                        {c.last_error && <p className="w-full break-words text-xs text-crit">{c.last_error}</p>}
                      </li>
                    ))}
                  </ul>
                  {caps && (
                    <div className="mt-3 flex flex-wrap gap-1.5 text-xs">
                      <Tag tone={caps.read_all_stats ? 'ok' : 'warn'}>pg_read_all_stats {caps.read_all_stats ? 'yes' : 'no'}</Tag>
                      <Tag tone={caps.pg_stat_statements ? 'ok' : 'muted'}>pg_stat_statements {caps.pg_stat_statements ? caps.pg_stat_statements_version ?? 'yes' : 'no'}</Tag>
                      <Tag tone={caps.track_io_timing ? 'ok' : 'muted'}>track_io_timing {caps.track_io_timing ? 'on' : 'off'}</Tag>
                      {caps.pg_stat_io && <Tag tone="ok">pg_stat_io</Tag>}
                    </div>
                  )}
                </Card>
                <HostCard id={id} />
              </div>
            </div>
            {current.length === 0 && d.findings.length === 0 && <Empty title="No rule results yet">Findings appear after the first collections.</Empty>}
          </>
        )
      }}
    </DataState>
  )
}
