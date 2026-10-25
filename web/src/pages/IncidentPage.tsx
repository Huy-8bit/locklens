import { useState } from 'react'
import { Link, useParams } from 'react-router'
import { ArrowLeft, Download } from 'lucide-react'
import { useAcknowledge, useIncident, useIncidentEvents } from '../api/hooks'
import type { Finding, IncidentEvent, Report } from '../api/types'
import { BlockingTree } from '../components/BlockingTree'
import { FindingCard } from '../components/FindingCard'
import { PageHeader } from '../components/Shell'
import { Badge, Button, Card, ErrorBox, inputCls, KeyValue, Load, Notice, SeverityBadge } from '../components/ui'
import { bytes, seconds, time } from '../lib/format'
import { api } from '../api/client'

const tone: Record<string, 'crit' | 'warn' | 'good' | 'accent' | 'muted'> = { trigger: 'crit', retriggered: 'crit', resolved: 'good', gap: 'warn', truncated: 'warn', recorder_restarted: 'warn', blocking_snapshot: 'accent', finding: 'accent' }

function Snapshot({ ev }: { ev: IncidentEvent }) {
  const [state, setState] = useState<{ busy?: boolean; report?: Report; error?: unknown }>({})
  if (state.report) return <div className="mt-2"><BlockingTree report={state.report} /></div>
  if (state.error) return <ErrorBox error={state.error} />
  return (
    <Button
      size="sm"
      className="mt-1"
      busy={state.busy}
      onClick={() => {
        setState({ busy: true })
        api<{ report: Report }>('/api/v1/snapshots/analyze', { method: 'POST', json: ev.payload.snapshot })
          .then((r) => setState({ report: r.report }))
          .catch((e) => setState({ error: e }))
      }}
    >
      Show blocking graph
    </Button>
  )
}

export default function IncidentPage() {
  const id = Number(useParams().incidentId)
  const q = useIncident(id)
  const events = useIncidentEvents(id)
  const ack = useAcknowledge(id)
  const [note, setNote] = useState('')
  const [includeQuery, setIncludeQuery] = useState(false)
  return (
    <Load q={q} label="Loading incident">
      {({ incident: i, findings }) => {
        const exp = (f: string) => `/api/v1/incidents/${i.id}/export?format=${f}${includeQuery ? '&include_query=true' : ''}`
        return (
          <>
            {i.target_id && (
              <Link to={`/i/${i.target_id}/alerts`} className="mb-2 inline-flex items-center gap-1 text-sm text-accent hover:underline">
                <ArrowLeft aria-hidden className="h-4 w-4" /> Alerts of {i.target_name}
              </Link>
            )}
            <PageHeader
              title={
                <span className="flex flex-wrap items-center gap-2">
                  <SeverityBadge severity={i.severity} /> #{i.id} {i.title}
                </span>
              }
              subtitle={`${i.target_name} · ${i.rule_id} v${i.rule_version} · ${i.state}`}
              actions={
                <>
                  <label className="inline-flex items-center gap-1.5 text-xs text-ink-2">
                    <input type="checkbox" checked={includeQuery} onChange={(e) => setIncludeQuery(e.target.checked)} />
                    include SQL text in export
                  </label>
                  <a href={exp('json')} download className="inline-flex items-center gap-1 rounded-lg border border-line-strong px-3 py-1.5 text-sm hover:bg-surface-2">
                    <Download aria-hidden className="h-4 w-4" /> Export JSON
                  </a>
                  <a href={exp('html')} download className="inline-flex items-center gap-1 rounded-lg border border-line-strong px-3 py-1.5 text-sm hover:bg-surface-2">
                    <Download aria-hidden className="h-4 w-4" /> Export HTML report
                  </a>
                </>
              }
            />
            {i.truncated && <Notice tone="warn">This incident reached its size limit; later samples were not stored.</Notice>}
            <div className="mt-3 grid gap-3 xl:grid-cols-3">
              <Card className="xl:col-span-2" title={`Timeline (${events.data?.events.length ?? '…'} events)`}>
                <Load q={events} label="Loading timeline">
                  {(e) => (
                    <ol className="space-y-3">
                      {e.events.map((ev) => (
                        <li key={ev.id} className="border-l-2 border-line-strong pl-3">
                          <div className="flex flex-wrap items-center gap-2 text-sm">
                            <span className="num text-xs text-muted">{time(ev.at, { date: true })}</span>
                            <Badge tone={tone[ev.kind] ?? 'muted'}>{ev.kind.replace(/_/g, ' ')}</Badge>
                            <span>{ev.summary}</span>
                          </div>
                          {ev.kind === 'blocking_snapshot' && !!ev.payload.snapshot && <Snapshot ev={ev} />}
                          {(ev.kind === 'trigger' || ev.kind === 'finding') && !!ev.payload.finding && (
                            <div className="mt-1.5">
                              <FindingCard finding={ev.payload.finding as Finding} open={ev.kind === 'trigger'} />
                            </div>
                          )}
                        </li>
                      ))}
                    </ol>
                  )}
                </Load>
              </Card>
              <div className="space-y-3">
                <Card title="Summary">
                  <KeyValue
                    items={[
                      ['Opened', time(i.opened_at, { date: true })],
                      ['Last seen', time(i.last_seen_at, { date: true })],
                      ['Resolved', i.resolved_at ? `${time(i.resolved_at, { date: true })} (${i.resolution ?? ''})` : 'still open'],
                      ['Duration', seconds((Date.parse(i.resolved_at ?? i.last_seen_at) - Date.parse(i.opened_at)) / 1000)],
                      ['Evidence', `${i.event_count} events · ${bytes(i.size_bytes)}`],
                      ['Source', i.source],
                    ]}
                  />
                </Card>
                <Card title="Acknowledgement">
                  {i.acknowledged_at ? (
                    <p className="text-sm">
                      Acknowledged by {i.acknowledged_by} at {time(i.acknowledged_at, { date: true })}
                      {i.ack_note ? `: “${i.ack_note}”` : ''}
                    </p>
                  ) : (
                    <form
                      className="space-y-2"
                      onSubmit={(e) => {
                        e.preventDefault()
                        ack.mutate(note)
                      }}
                    >
                      <label className="block text-sm">
                        <span className="mb-1 block text-xs text-muted">Note (metadata only; evidence is never changed)</span>
                        <textarea className={inputCls} rows={2} value={note} onChange={(e) => setNote(e.target.value)} maxLength={1000} />
                      </label>
                      {ack.error && <ErrorBox error={ack.error} />}
                      <Button type="submit" busy={ack.isPending}>
                        Acknowledge
                      </Button>
                    </form>
                  )}
                </Card>
                {findings.length > 0 && (
                  <Card title={`Linked findings (${findings.length})`}>
                    <div className="space-y-2">
                      {findings.map((f) => (
                        <FindingCard key={f.id} finding={f.finding} />
                      ))}
                    </div>
                  </Card>
                )}
              </div>
            </div>
          </>
        )
      }}
    </Load>
  )
}
