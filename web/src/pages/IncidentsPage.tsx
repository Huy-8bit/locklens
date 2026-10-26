import { useState } from 'react'
import { Link } from 'react-router'
import { useFleet, useIncidents } from '../api/hooks'
import { PageHeader } from '../components/Shell'
import { Pager, tableCls, tdCls, Th, trCls } from '../components/Table'
import { Badge, Card, Empty, Load, Segmented, SeverityBadge } from '../components/ui'
import { ago, seconds, time } from '../lib/format'
import { useScope } from '../lib/scope'

/** Incidents of every instance; the environment filter applies to the current page. */
export default function IncidentsPage() {
  const { env } = useScope()
  const [state, setState] = useState<'open' | 'all'>('open')
  const [page, setPage] = useState(1)
  const incidents = useIncidents({ state: state === 'open' ? 'open' : undefined, page })
  const fleet = useFleet()
  const envOf = new Map((fleet.data?.targets ?? []).map((e) => [e.target.id, e.target.environment]))
  return (
    <>
      <PageHeader title="Incidents" subtitle="Recorded automatically when a rule keeps firing, with the evidence collected at the time. Possible explanations are never presented as confirmed causes." />
      <Card
        pad={false}
        title={state === 'open' ? 'Open incidents' : 'All incidents'}
        actions={
          <Segmented
            label="Incident state"
            value={state}
            onChange={(v) => {
              setState(v)
              setPage(1)
            }}
            items={[
              { value: 'open', label: 'Open' },
              { value: 'all', label: 'All' },
            ]}
          />
        }
      >
        <Load q={incidents} label="Loading incidents">
          {(d) => {
            const rows = d.incidents.filter((i) => !env || (i.target_id !== null && envOf.get(i.target_id) === env))
            if (rows.length === 0)
              return (
                <div className="px-5 pb-5">
                  <Empty title={state === 'open' ? 'No open incidents' : 'No incidents recorded'}>Incidents open for sustained blocking, connection pressure, slower queries, or WAL retained by a slot.</Empty>
                </div>
              )
            return (
              <>
                <div className="overflow-x-auto">
                  <table className={tableCls}>
                    <caption className="sr-only">Incidents</caption>
                    <thead>
                      <tr>
                        <Th className="pl-5">Incident</Th>
                        <Th>Instance</Th>
                        <Th>State</Th>
                        <Th>Opened</Th>
                        <Th>Duration</Th>
                        <Th className="pr-5">Acknowledged</Th>
                      </tr>
                    </thead>
                    <tbody>
                      {rows.map((i) => (
                        <tr key={i.id} className={trCls}>
                          <td className={`${tdCls} pl-5`}>
                            <Link to={`/incidents/${i.id}`} className="flex items-center gap-2 hover:underline">
                              <SeverityBadge severity={i.severity} />
                              <span>
                                #{i.id} {i.title}
                              </span>
                            </Link>
                          </td>
                          <td className={`${tdCls} whitespace-nowrap`}>{i.target_id ? <Link to={`/databases/${i.target_id}/alerts`} className="hover:underline">{i.target_name}</Link> : i.target_name || '—'}</td>
                          <td className={tdCls}>{i.state === 'open' ? <Badge tone="crit">open</Badge> : <Badge tone="good">resolved</Badge>}</td>
                          <td className={`${tdCls} num whitespace-nowrap text-xs`} title={time(i.opened_at, { date: true })}>
                            {ago(i.opened_at)}
                          </td>
                          <td className={`${tdCls} num text-xs`}>{seconds((Date.parse(i.resolved_at ?? i.last_seen_at) - Date.parse(i.opened_at)) / 1000)}</td>
                          <td className={`${tdCls} pr-5 text-xs text-muted`}>{i.acknowledged_at ? `${i.acknowledged_by ?? 'yes'} · ${ago(i.acknowledged_at)}` : '—'}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                {d.total > d.page_size && <Pager page={page} pageSize={d.page_size} total={d.total} onPage={setPage} />}
              </>
            )
          }}
        </Load>
      </Card>
    </>
  )
}
