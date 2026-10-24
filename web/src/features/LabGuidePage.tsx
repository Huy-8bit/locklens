import { Card, PageHeader } from '../components/ui'

const SCENARIOS: [string, string, string][] = [
  ['healthy', 'Light background workload (reads and small updates).', 'Fleet and overview charts show steady commits; no findings.'],
  ['row-blocking', 'Session A updates a row and keeps working; B updates the same row and waits.', 'Blocking explorer shows A → B; after the persistence threshold a "blocking.long_chain" incident opens.'],
  ['chain', 'A holds row 1; B holds row 2 and waits for row 1; C waits for row 2.', 'A → B → C with A blocking 1 directly and 2 in total.'],
  ['idle-blocker', 'A updates a row and stays idle in transaction while B and C wait.', 'An "idle-in-transaction blocker" incident (critical).'],
  ['connection-pressure', 'Opens idle connections up to about 90% of available slots.', 'A "connection pressure" incident.'],
  ['regression', 'Runs a query with an index, then drops the index so the same query gets slower; restores it at the end.', 'Query workload shows "slower per call"; a regression incident opens after the window comparison.'],
  ['temp-files', 'Sorts with a tiny work_mem so data spills to temporary files.', 'Temporary-file chart rises; a temp-files finding appears.'],
  ['replication', 'Pauses replay on the standby and creates an inactive slot while writing WAL.', 'Replication view shows growing replay delay and retained WAL; a slot incident opens. Replay and the slot are restored at the end.'],
  ['outage', 'Stops the primary for a while, then starts it again (run from the host).', 'The target turns unreachable, a collection gap is recorded, then it recovers.'],
]

export function LabGuidePage() {
  return (
    <>
      <PageHeader title="Lab guide" subtitle="Demo mode: scenarios run outside LockLens through a separate runner. They use fake data and clean up after themselves." />
      <div className="space-y-4">
        <Card title="Running a scenario">
          <pre className="overflow-x-auto rounded-md bg-panel2 p-3 font-mono text-xs">{`make lab SCENARIO=chain          # runs for 3 minutes, then cleans up
make lab SCENARIO=chain DURATION=10m
make lab-status                  # what the lab is doing now
make lab-stop                    # stop every lab session and clean up
make lab-outage                  # stop and restart the primary`}</pre>
          <p className="mt-2 text-sm text-muted">LockLens never runs workloads itself; there is no endpoint for it. Demo thresholds are shorter than production defaults so findings appear within minutes.</p>
        </Card>
        <Card title="Scenarios">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left text-xs text-muted">
                <th className="py-1 pr-3 font-medium">Scenario</th>
                <th className="py-1 pr-3 font-medium">What it does</th>
                <th className="py-1 font-medium">What to look for</th>
              </tr>
            </thead>
            <tbody>
              {SCENARIOS.map(([name, what, look]) => (
                <tr key={name} className="border-t border-border align-top">
                  <td className="py-2 pr-3 font-mono">{name}</td>
                  <td className="py-2 pr-3">{what}</td>
                  <td className="py-2 text-muted">{look}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      </div>
    </>
  )
}
