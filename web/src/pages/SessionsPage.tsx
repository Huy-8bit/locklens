import { useFleetLive } from '../api/hooks'
import { PageHeader } from '../components/Shell'
import { Card, Empty, Load, Notice, Stat } from '../components/ui'
import { SessionsTable } from '../components/widgets/SessionsTable'
import { ago, num } from '../lib/format'
import { useScope } from '../lib/scope'

export default function SessionsPage() {
  const { env } = useScope()
  const live = useFleetLive(env)
  return (
    <>
      <PageHeader title="Sessions" subtitle="Client sessions from the latest activity sample of each instance. Read-only: LockLens never cancels queries or terminates sessions." />
      <Load q={live} label="Loading sessions">
        {(d) => {
          const total = (k: 'client_total' | 'active' | 'idle_in_transaction' | 'waiting_lock') => (d.targets.some((t) => t.counts) ? d.targets.reduce((n, t) => n + (t.counts?.[k] ?? 0), 0) : null)
          const blocked = d.targets.reduce((n, t) => n + (t.report?.analysis.summary.blocked_sessions ?? 0), 0)
          const stale = d.targets.filter((t) => !t.connected)
          const hidden = d.targets.filter((t) => t.report?.snapshot.warnings.some((w) => w.code === 'restricted_visibility'))
          if (d.targets.length === 0) return <Empty title="No enabled instance in this environment" />
          return (
            <div className="space-y-4">
              <div className="grid grid-cols-2 gap-3 md:grid-cols-5">
                <Stat label="Client sessions" value={num(total('client_total'))} />
                <Stat label="Running queries" value={num(total('active'))} />
                <Stat label="Idle in transaction" value={num(total('idle_in_transaction'))} tone={total('idle_in_transaction') ? 'warn' : undefined} sub="holding a transaction open" />
                <Stat label="Waiting for a lock" value={num(total('waiting_lock'))} tone={total('waiting_lock') ? 'crit' : undefined} />
                <Stat label="Blocked sessions" value={num(blocked)} tone={blocked ? 'crit' : undefined} />
              </div>
              {stale.length > 0 && <Notice tone="warn">Not connected right now: {stale.map((t) => `${t.target.name} (sample from ${ago(t.observed_at)})`).join(', ')}.</Notice>}
              {hidden.length > 0 && <Notice tone="warn">Some sessions are hidden from the monitoring role on {hidden.map((t) => t.target.name).join(', ')} (grant pg_read_all_stats for full detail).</Notice>}
              <Card pad={false} title="Sessions" subtitle="Plain idle connections are counted above; filter by state or search by query, user, application, or PID. At most 200 sessions per instance are shown.">
                <SessionsTable targets={d.targets} />
              </Card>
            </div>
          )
        }}
      </Load>
    </>
  )
}
