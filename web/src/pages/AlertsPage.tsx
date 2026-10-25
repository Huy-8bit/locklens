import { useState } from 'react'
import { Link } from 'react-router'
import { useFindings, useIncidents } from '../api/hooks'
import { FindingCard } from '../components/FindingCard'
import { SectionHeader, useNumParam } from '../components/Shell'
import { tableCls, tdCls, Th, trCls } from '../components/Table'
import { Badge, Card, Empty, Load, Segmented, SeverityBadge } from '../components/ui'
import { seconds, time } from '../lib/format'

export default function AlertsPage() {
  const id = useNumParam('id')
  const [state, setState] = useState<'open' | 'all'>('open')
  const incidents = useIncidents({ target: id, state: state === 'open' ? 'open' : undefined })
  const findings = useFindings(id)
  return (
    <>
      <SectionHeader title="Alerts" subtitle="Incidents are recorded automatically when a problem persists; findings describe what the rules observe, with evidence. Possible explanations are never presented as confirmed causes." />
      <div className="space-y-3">
        <Card pad={false} title="Incidents" actions={<Segmented label="Incident state" value={state} onChange={setState} items={[{ value: 'open', label: 'Open' }, { value: 'all', label: 'All' }]} />}>
          <Load q={incidents} label="Loading incidents">
            {(d) =>
              d.incidents.length === 0 ? (
                <div className="px-4 pb-4">
                  <Empty title={state === 'open' ? 'No open incidents' : 'No incidents recorded'}>Incidents open for sustained blocking, connection pressure, slower queries, or WAL retained by a slot.</Empty>
                </div>
              ) : (
                <div className="overflow-x-auto">
                  <table className={tableCls}>
                    <thead>
                      <tr>
                        <Th>Incident</Th>
                        <Th>State</Th>
                        <Th>Opened</Th>
                        <Th>Duration</Th>
                      </tr>
                    </thead>
                    <tbody>
                      {d.incidents.map((i) => (
                        <tr key={i.id} className={trCls}>
                          <td className={tdCls}>
                            <Link to={`/incidents/${i.id}`} className="flex items-center gap-2 hover:underline">
                              <SeverityBadge severity={i.severity} />
                              <span>
                                #{i.id} {i.title}
                              </span>
                            </Link>
                          </td>
                          <td className={tdCls}>{i.state === 'open' ? <Badge tone="crit">open</Badge> : <Badge tone="good">resolved</Badge>}</td>
                          <td className={`${tdCls} num text-xs`}>{time(i.opened_at, { date: true })}</td>
                          <td className={`${tdCls} num text-xs`}>{seconds((Date.parse(i.resolved_at ?? i.last_seen_at) - Date.parse(i.opened_at)) / 1000)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )
            }
          </Load>
        </Card>
        <Card title="Current findings">
          <Load q={findings} label="Loading findings">
            {(d) => {
              const active = d.findings.filter((f) => f.finding.status === 'active')
              const waiting = d.findings.filter((f) => f.finding.status === 'insufficient_data')
              return (
                <div className="space-y-2">
                  {active.length === 0 && <p className="text-sm text-muted">No rule is firing right now.</p>}
                  {active.map((f) => (
                    <FindingCard key={f.id} finding={f.finding} />
                  ))}
                  {waiting.length > 0 && (
                    <details className="pt-2">
                      <summary className="cursor-pointer text-sm text-muted">{waiting.length} rule(s) not evaluated (insufficient data)</summary>
                      <div className="mt-2 space-y-2">
                        {waiting.map((f) => (
                          <FindingCard key={f.id} finding={f.finding} />
                        ))}
                      </div>
                    </details>
                  )}
                </div>
              )
            }}
          </Load>
        </Card>
      </div>
    </>
  )
}
