import { useFleetLive } from '../api/hooks'
import { BlockingTree } from '../components/BlockingTree'
import { PageHeader } from '../components/Shell'
import { tableCls, tdCls, tdNum, Th, trCls } from '../components/Table'
import { Badge, Card, Empty, Load, Stat } from '../components/ui'
import { BlockingGraph } from '../components/widgets/BlockingGraph'
import { num, seconds } from '../lib/format'
import { useScope } from '../lib/scope'

export default function BlockingPage() {
  const { env } = useScope()
  const live = useFleetLive(env)
  return (
    <>
      <PageHeader title="Blocking" subtitle="Who waits on whose lock, from pg_blocking_pids() in the latest sample. A cycle is shown as a cycle: PostgreSQL detects and resolves real deadlocks itself." />
      <Load q={live} label="Loading lock waits">
        {(d) => {
          if (d.targets.length === 0) return <Empty title="No enabled instance in this environment" />
          const s = (k: 'blocked_sessions' | 'root_blockers' | 'long_transactions' | 'idle_in_transaction') => d.targets.reduce((n, t) => n + (t.report?.analysis.summary[k] ?? 0), 0)
          const withTrees = d.targets.flatMap((t) => (t.report && t.report.analysis.components.length > 0 ? [{ t, report: t.report }] : []))
          const long = d.targets.flatMap((t) => (t.report?.analysis.long_transactions ?? []).map((l) => ({ t, l })))
          return (
            <div className="space-y-4">
              <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
                <Stat label="Blocked sessions" value={num(s('blocked_sessions'))} tone={s('blocked_sessions') ? 'crit' : undefined} />
                <Stat label="Root blockers" value={num(s('root_blockers'))} tone={s('root_blockers') ? 'crit' : undefined} />
                <Stat label="Long transactions" value={num(s('long_transactions'))} tone={s('long_transactions') ? 'warn' : undefined} />
                <Stat label="Idle in transaction" value={num(s('idle_in_transaction'))} tone={s('idle_in_transaction') ? 'warn' : undefined} />
              </div>
              <Card title="Blocking transactions" subtitle="Each blocker and the sessions waiting behind it">
                <BlockingGraph targets={d.targets} />
              </Card>
              {withTrees.map(({ t, report }) => (
                <Card key={t.target.id} title={`Blocking tree · ${t.target.name}`} subtitle="Every level of waiting, with lock modes and the objects involved">
                  <BlockingTree report={report} />
                </Card>
              ))}
              {long.length > 0 && (
                <Card pad={false} title="Long-running transactions" subtitle="Open transactions older than the report threshold; they can hold locks and keep vacuum from cleaning up">
                  <div className="overflow-x-auto">
                    <table className={tableCls}>
                      <thead>
                        <tr>
                          <Th className="pl-5">Instance</Th>
                          <Th>PID</Th>
                          <Th>State</Th>
                          <Th align="right">Transaction age</Th>
                          <Th align="right" className="pr-5">
                            Blocks
                          </Th>
                        </tr>
                      </thead>
                      <tbody>
                        {long.map(({ t, l }) => (
                          <tr key={`${t.target.id}-${l.pid}`} className={trCls}>
                            <td className={`${tdCls} pl-5`}>{t.target.name}</td>
                            <td className={`${tdCls} font-mono`}>{l.pid}</td>
                            <td className={tdCls}>{l.idle_in_transaction ? <Badge tone="warn">idle in transaction</Badge> : (l.state ?? '—')}</td>
                            <td className={tdNum}>{seconds(l.transaction_age_seconds)}</td>
                            <td className={`${tdNum} pr-5`}>{num(l.direct_blocked_count)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </Card>
              )}
            </div>
          )
        }}
      </Load>
    </>
  )
}
