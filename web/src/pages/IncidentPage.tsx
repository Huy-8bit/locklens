import { useMemo, useState } from 'react'
import { Link, useParams } from 'react-router'
import { ArrowLeft, Download } from 'lucide-react'
import { useAcknowledge, useIncident, useIncidentEvents } from '../api/hooks'
import { useCan } from '../auth'
import type { Finding, IncidentEvent, QueryRef, WorkloadEntry } from '../api/types'
import { FindingCard } from '../components/FindingCard'
import { BlockingCard, ContextCharts, IncidentBrief, LoadSummaryCard, SessionsCard, StatementCard, Timeline, WorkloadCard } from '../components/IncidentParts'
import { LoadPanel } from '../components/LoadPanel'
import { IncidentNotifications } from '../components/NotificationsCard'
import { PageHeader } from '../components/Shell'
import { Badge, Button, Card, ErrorBox, inputCls, KeyValue, Load, Notice, SeverityBadge, Spinner } from '../components/ui'
import { bytes, seconds, time } from '../lib/format'
import { evidenceNumber, findingQueryKey, incidentWindow, queryKey, ruleInfo } from '../lib/incident'
import { absolute } from '../lib/scope'

export default function IncidentPage() {
  const id = Number(useParams().incidentId)
  const q = useIncident(id)
  const events = useIncidentEvents(id)
  const ack = useAcknowledge(id)
  const can = useCan()
  const [note, setNote] = useState('')
  const [includeQuery, setIncludeQuery] = useState(false)

  // Evidence by kind, and the statements the evidence names.
  const parts = useMemo(() => {
    const evs = events.data?.events ?? []
    const of = (kind: string) => evs.filter((e) => e.kind === kind)
    const triggers = of('trigger')
      .map((e) => e.payload.finding as Finding | undefined)
      .filter((f): f is Finding => !!f)
    const refs = new Map<string, QueryRef>((events.data?.statements ?? []).map((r) => [queryKey(r.key), r]))
    return { all: evs, triggers, refs, workload: of('workload'), sessions: of('sessions')[0] as IncidentEvent | undefined, load: of('load')[0] as IncidentEvent | undefined, blocking: of('blocking_snapshot') }
  }, [events.data])

  return (
    <Load q={q} label="Loading incident">
      {({ incident: i, findings }) => {
        const exp = (f: string) => `/api/v1/incidents/${i.id}/export?format=${f}${includeQuery ? '&include_query=true' : ''}`
        const info = ruleInfo(i.rule_id)
        const lead = parts.triggers.find((f) => f.rule_id === i.rule_id) ?? parts.triggers[0] ?? findings[0]?.finding
        const leadKey = findingQueryKey(lead)
        const firstWorkload = parts.workload[0]?.payload.current as WorkloadEntry[] | undefined
        const share = leadKey ? (firstWorkload?.find((w) => queryKey(w.key) === leadKey)?.share_of_total_time ?? null) : null
        const { from, to } = incidentWindow(i)
        const rootPid = evidenceNumber(lead, 'root pid')
        return (
          <>
            <Link to={i.target_id ? `/databases/${i.target_id}/alerts` : '/incidents'} className="mb-2 inline-flex items-center gap-1 text-sm text-accent hover:underline">
              <ArrowLeft aria-hidden className="h-4 w-4" /> {i.target_id ? `Alerts of ${i.target_name}` : 'Incidents'}
            </Link>
            <PageHeader
              title={
                <>
                  <span className="text-muted">#{i.id}</span> {i.title}
                </>
              }
              subtitle={
                <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
                  <SeverityBadge severity={i.severity} />
                  <Badge tone={i.state === 'open' ? 'crit' : 'good'}>{i.state === 'open' ? 'open' : 'resolved'}</Badge>
                  <span>
                    {i.target_name} · {info.name} · opened {time(i.opened_at, { date: true })} · {i.state === 'open' ? 'open for' : 'lasted'} {seconds((to - from) / 1000)}
                  </span>
                </span>
              }
              actions={
                <>
                  <label className="inline-flex items-center gap-1.5 text-xs text-ink-2">
                    <input type="checkbox" checked={includeQuery} onChange={(e) => setIncludeQuery(e.target.checked)} />
                    include SQL text in export
                  </label>
                  <a href={exp('json')} download aria-label="Export JSON" className="inline-flex items-center gap-1 rounded-lg border border-line-strong px-3 py-1.5 text-sm hover:bg-surface-2">
                    <Download aria-hidden className="h-4 w-4" /> JSON
                  </a>
                  <a href={exp('html')} download aria-label="Export HTML report" className="inline-flex items-center gap-1 rounded-lg border border-line-strong px-3 py-1.5 text-sm hover:bg-surface-2">
                    <Download aria-hidden className="h-4 w-4" /> HTML report
                  </a>
                </>
              }
            />
            {i.truncated && <Notice tone="warn">This incident reached its size limit; later samples were not stored.</Notice>}
            <div className="mt-3 grid gap-3 xl:grid-cols-3">
              <div className="min-w-0 space-y-3 xl:col-span-2">
                <IncidentBrief incident={i} trigger={lead} share={share} />
                {events.error ? (
                  <ErrorBox error={events.error} title="Could not load the recorded evidence" />
                ) : !events.data ? (
                  <Spinner label="Loading evidence" />
                ) : (
                  <>
                    {lead && leadKey && <StatementCard incident={i} trigger={lead} statement={parts.refs.get(leadKey)} />}
                    {parts.blocking.length > 0 && <BlockingCard events={parts.blocking} />}
                    {parts.sessions && <SessionsCard event={parts.sessions} rootPid={rootPid} />}
                    {parts.load && <LoadSummaryCard event={parts.load} refs={parts.refs} targetId={i.target_id} />}
                    {i.target_id && (
                      <LoadPanel
                        targetId={i.target_id}
                        range={absolute(from - 30 * 60_000, to + 10 * 60_000)}
                        title="Database load around the incident"
                        subtitle="Average active sessions from 30 minutes before the incident until 10 minutes after it, by statement; the incident is shaded. Group by wait class to see what the sessions waited on."
                        zoomable={false}
                        highlight={{ from, to }}
                        initial="query"
                        rows={8}
                      />
                    )}
                    {parts.workload.length > 0 && <WorkloadCard events={parts.workload} refs={parts.refs} targetId={i.target_id} triggerKey={leadKey} />}
                    {i.target_id && <ContextCharts incident={i} />}
                    <Card title="Timeline" subtitle={`${parts.all.length} recorded events${events.data.has_more ? ' (the first 2,000)' : ''}. Identical routine samples are collapsed; click to expand them.`}>
                      <Timeline events={parts.all} />
                    </Card>
                  </>
                )}
              </div>
              <div className="min-w-0 space-y-3">
                <Card title="Summary">
                  <KeyValue
                    items={[
                      ['Instance', i.target_id ? <Link to={`/databases/${i.target_id}`} className="text-accent hover:underline">{i.target_name}</Link> : i.target_name],
                      ['Opened', time(i.opened_at, { date: true })],
                      ['Last seen', time(i.last_seen_at, { date: true })],
                      ['Resolved', i.resolved_at ? `${time(i.resolved_at, { date: true })} (${i.resolution ?? ''})` : 'still open'],
                      ['Rule', `${i.rule_id} v${i.rule_version}`],
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
                  ) : !can('incidents.acknowledge') ? (
                    <p className="text-sm text-muted">Not acknowledged. Operators and administrators can acknowledge incidents.</p>
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
                {i.source === 'recorder' && <IncidentNotifications incidentId={i.id} targetId={i.target_id} ruleId={i.rule_id} />}
                {lead && (
                  <Card title="Rule evidence" subtitle="Every value the rule used, its source, possible explanations (not confirmed causes), and what to check next.">
                    <FindingCard finding={lead} open />
                  </Card>
                )}
                {findings.length > 1 && (
                  <Card title={`Other findings (${findings.length - 1})`}>
                    <div className="space-y-2">
                      {findings
                        .filter((f) => f.finding.key !== lead?.key || f.finding.rule_id !== lead?.rule_id)
                        .map((f) => (
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
