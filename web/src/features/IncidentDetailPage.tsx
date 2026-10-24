import { useCallback, useState } from 'react'
import { Link, useParams } from 'react-router'
import { api } from '../api/client'
import { useAcknowledge, useIncident, useIncidentEvents } from '../api/hooks'
import type { ActivityResponse, Finding, IncidentEvent } from '../api/types'
import { BlockingTree } from '../components/BlockingTree'
import { FindingCard } from '../components/FindingCard'
import { Button, Card, DataState, ErrorBox, KeyValue, Notice, PageHeader, SeverityBadge, Spinner, Tag } from '../components/ui'
import { fmtBytes, fmtDuration, fmtNumber, fmtTime } from '../lib/format'

function SnapshotEvent({ ev }: { ev: IncidentEvent }) {
  const [state, setState] = useState<{ loading?: boolean; data?: ActivityResponse; error?: unknown }>({})
  const [selected, setSelected] = useState<number | null>(null)
  const onSelect = useCallback((pid: number) => setSelected(pid), [])
  const load = async () => {
    setState({ loading: true })
    try {
      const data = await api<ActivityResponse>('/api/v1/snapshots/analyze', { method: 'POST', body: JSON.stringify(ev.payload.snapshot), headers: { 'Content-Type': 'application/json' } })
      setState({ data })
    } catch (e) {
      setState({ error: e })
    }
  }
  if (!state.data && !state.loading && !state.error) {
    return (
      <Button onClick={() => void load()} className="mt-1">
        Show blocking graph
      </Button>
    )
  }
  if (state.loading) return <Spinner label="Rendering graph" />
  if (state.error) return <ErrorBox error={state.error} />
  return state.data?.report ? (
    <div className="mt-2">
      <BlockingTree report={state.data.report} selected={selected} onSelect={onSelect} />
    </div>
  ) : null
}

function Metrics({ m }: { m: Record<string, number | null> }) {
  const keys = Object.keys(m).sort()
  return (
    <details className="mt-1 text-xs">
      <summary className="cursor-pointer text-muted">metrics at this time</summary>
      <dl className="mt-1 grid grid-cols-[max-content_1fr] gap-x-3">
        {keys.map((k) => (
          <div key={k} className="contents">
            <dt className="font-mono text-muted">{k}</dt>
            <dd className="font-mono">{m[k] === null || m[k] === undefined ? 'unknown' : fmtNumber(m[k], 4)}</dd>
          </div>
        ))}
      </dl>
    </details>
  )
}

interface TopQuery {
  key: { queryid: string }
  datname: string
  username: string
  text?: string
  calls: number
  total_exec_ms: number
  mean_exec_ms: number | null
  share_of_total_time: number | null
}

function EventBody({ ev }: { ev: IncidentEvent }) {
  const p = ev.payload
  if (ev.kind === 'blocking_snapshot' && p.snapshot) return <SnapshotEvent ev={ev} />
  if ((ev.kind === 'trigger' || ev.kind === 'finding') && p.finding) {
    return (
      <ul className="mt-1">
        <FindingCard finding={p.finding as Finding} />
      </ul>
    )
  }
  if (['sample', 'pre_trigger', 'post_trigger'].includes(ev.kind) && p.metrics) return <Metrics m={p.metrics as Record<string, number | null>} />
  if (ev.kind === 'workload' && Array.isArray(p.current)) {
    const rows = p.current as TopQuery[]
    return (
      <div className="mt-1 overflow-x-auto">
        <table className="w-full min-w-[560px] text-xs">
          <thead>
            <tr className="text-left text-muted">
              <th className="font-medium">queryid</th>
              <th className="font-medium">database · user</th>
              <th className="text-right font-medium">calls</th>
              <th className="text-right font-medium">total</th>
              <th className="text-right font-medium">mean</th>
            </tr>
          </thead>
          <tbody className="tabular">
            {rows.map((r) => (
              <tr key={r.key.queryid + r.datname} className="border-t border-border">
                <td className="font-mono">{r.key.queryid}</td>
                <td>
                  {r.datname} · {r.username}
                </td>
                <td className="text-right">{fmtNumber(r.calls)}</td>
                <td className="text-right">{fmtNumber(r.total_exec_ms, 1)} ms</td>
                <td className="text-right">{r.mean_exec_ms === null ? '—' : `${fmtNumber(r.mean_exec_ms, 2)} ms`}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    )
  }
  return null
}

const kindTone: Record<string, 'crit' | 'warn' | 'ok' | 'info' | 'muted'> = {
  trigger: 'crit',
  retriggered: 'crit',
  resolved: 'ok',
  gap: 'warn',
  truncated: 'warn',
  visibility: 'warn',
  recorder_restarted: 'warn',
  blocking_snapshot: 'info',
  finding: 'info',
  workload: 'info',
}

export function IncidentDetailPage() {
  const id = Number(useParams().id)
  const q = useIncident(id)
  const events = useIncidentEvents(id)
  const ack = useAcknowledge(id)
  const [note, setNote] = useState('')
  const [includeQuery, setIncludeQuery] = useState(false)
  return (
    <DataState query={q} loadingLabel="Loading incident">
      {(d) => {
        const i = d.incident
        const end = i.resolved_at ?? i.last_seen_at
        const exportUrl = (format: string) => `/api/v1/incidents/${i.id}/export?format=${format}${includeQuery ? '&include_query=true' : ''}`
        return (
          <>
            <PageHeader
              title={
                <span className="flex flex-wrap items-center gap-2">
                  <SeverityBadge severity={i.severity} /> #{i.id} {i.title}
                </span>
              }
              subtitle={
                <>
                  {i.target_id ? <Link className="text-accent underline" to={`/targets/${i.target_id}`}>{i.target_name}</Link> : i.target_name} · rule <code>{i.rule_id}</code> v{i.rule_version} · {i.state}
                </>
              }
              actions={
                <div className="flex flex-wrap items-center gap-2 text-sm">
                  <label className="flex items-center gap-1.5">
                    <input type="checkbox" checked={includeQuery} onChange={(e) => setIncludeQuery(e.target.checked)} />
                    include query text in export
                  </label>
                  <a className="rounded-md border border-border px-3 py-1.5 hover:bg-panel2" href={exportUrl('json')} download>
                    Export JSON
                  </a>
                  <a className="rounded-md border border-border px-3 py-1.5 hover:bg-panel2" href={exportUrl('html')} download>
                    Export HTML report
                  </a>
                </div>
              }
            />
            {i.truncated && (
              <div className="mb-4">
                <Notice tone="warn">This incident reached its size limit; later samples were not stored, lifecycle events were.</Notice>
              </div>
            )}
            <div className="grid gap-4 xl:grid-cols-3">
              <div className="space-y-4 xl:col-span-2">
                <Card title={`Timeline (${events.data?.events.length ?? '…'} events)`}>
                  <DataState query={events} loadingLabel="Loading timeline">
                    {(e) => (
                      <ol className="space-y-3">
                        {e.events.map((ev) => (
                          <li key={ev.id} className="border-l-2 border-border pl-3">
                            <div className="flex flex-wrap items-center gap-2 text-sm">
                              <span className="tabular text-muted">{fmtTime(ev.at)}</span>
                              <Tag tone={kindTone[ev.kind] ?? 'muted'}>{ev.kind.replace(/_/g, ' ')}</Tag>
                              <span>{ev.summary}</span>
                            </div>
                            <EventBody ev={ev} />
                          </li>
                        ))}
                      </ol>
                    )}
                  </DataState>
                </Card>
              </div>
              <div className="space-y-4">
                <Card title="Summary">
                  <KeyValue
                    items={[
                      ['Opened', fmtTime(i.opened_at)],
                      ['Last seen', fmtTime(i.last_seen_at)],
                      ['Resolved', i.resolved_at ? `${fmtTime(i.resolved_at)} (${i.resolution ?? ''})` : 'still open'],
                      ['Duration', fmtDuration((Date.parse(end) - Date.parse(i.opened_at)) / 1000)],
                      ['Events', `${i.event_count} · ${fmtBytes(i.size_bytes)}`],
                      ['Source', i.source],
                    ]}
                  />
                  <p className="mt-3 text-xs text-muted">Findings describe observations and possible explanations, not confirmed root causes. Data before the trigger exists only if collection was running then.</p>
                </Card>
                <Card title="Acknowledgement">
                  {i.acknowledged_at ? (
                    <p className="text-sm">
                      Acknowledged by {i.acknowledged_by} at {fmtTime(i.acknowledged_at)}
                      {i.ack_note ? `: ${i.ack_note}` : '.'}
                    </p>
                  ) : (
                    <p className="text-sm text-muted">Not acknowledged.</p>
                  )}
                  <form
                    className="mt-3 space-y-2"
                    onSubmit={(e) => {
                      e.preventDefault()
                      ack.mutate(note, { onSuccess: () => setNote('') })
                    }}
                  >
                    <label className="block text-sm">
                      <span className="mb-1 block text-muted">Note (optional, metadata only)</span>
                      <textarea className="w-full rounded-md border border-border bg-bg p-2" maxLength={1000} rows={2} value={note} onChange={(e) => setNote(e.target.value)} />
                    </label>
                    {ack.error && <ErrorBox error={ack.error} title="Could not acknowledge" />}
                    <Button type="submit" busy={ack.isPending}>
                      {i.acknowledged_at ? 'Update acknowledgement' : 'Acknowledge'}
                    </Button>
                  </form>
                </Card>
                <Card title={`Linked findings (${d.findings.length})`}>
                  {d.findings.length === 0 ? (
                    <p className="text-sm text-muted">No findings linked.</p>
                  ) : (
                    <ul className="space-y-2">
                      {d.findings.map((f) => (
                        <FindingCard key={f.id} finding={f.finding} compact />
                      ))}
                    </ul>
                  )}
                </Card>
              </div>
            </div>
          </>
        )
      }}
    </DataState>
  )
}
