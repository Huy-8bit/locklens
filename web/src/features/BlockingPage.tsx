import { useCallback, useState } from 'react'
import { useSearchParams } from 'react-router'
import { useActivity, useSnapshotList } from '../api/hooks'
import { api } from '../api/client'
import type { ActivityResponse, Report } from '../api/types'
import { BlockingTree } from '../components/BlockingTree'
import { TimeRangePicker } from '../components/TimeRangePicker'
import { Card, DataState, ErrorBox, KeyValue, Notice, PageHeader, Tag } from '../components/ui'
import { fmtDuration, fmtTime } from '../lib/format'
import { describeLock } from '../lib/locks'
import { useTimeRange } from '../lib/time'
import { useTargetId } from './TargetOverviewPage'

function NodeDetails({ report, pid }: { report: Report; pid: number }) {
  const s = report.snapshot.sessions.find((x) => x.pid === pid)
  const n = report.analysis.nodes.find((x) => x.id === pid)
  const a = report.analysis.sessions.find((x) => x.pid === pid)
  if (pid === 0) {
    return (
      <div className="space-y-2 text-sm">
        <p>A prepared (two-phase) transaction holds the conflicting lock. PostgreSQL reports it as PID 0 and does not say which one.</p>
        <ul className="list-disc pl-5">
          {report.snapshot.prepared_transactions.map((p) => (
            <li key={p.gid}>
              <code>{p.gid}</code> prepared {fmtTime(p.prepared)} by {p.owner} in {p.database}
            </li>
          ))}
        </ul>
      </div>
    )
  }
  if (!s) return <p className="text-sm text-muted">pid {pid} has no pg_stat_activity row in this collection; its metadata is unknown.</p>
  return (
    <div className="space-y-3">
      {!s.details_visible && <Notice tone="warn">Details of this session are hidden from the monitoring role (no pg_read_all_stats).</Notice>}
      <KeyValue
        items={[
          ['PID', String(s.pid)],
          ['State', s.state ?? 'unknown'],
          ['Wait event', s.wait_event_type ? `${s.wait_event_type}: ${s.wait_event ?? ''}` : s.details_visible ? 'none' : 'unknown'],
          ['User / database', `${s.user ?? 'unknown'} / ${s.database ?? 'unknown'}`],
          ['Application', s.application_name || '—'],
          ['Client', s.client_addr ?? 'unknown or local socket'],
          ['Transaction age', fmtDuration(a?.transaction_age_seconds, 'no open transaction or unknown')],
          ['Query age', `${fmtDuration(a?.query_age_seconds)} (since the current or last statement started)`],
          ['Lock wait', `${fmtDuration(a?.lock_wait_seconds, n?.blocked_by.length ? 'unknown (no waitstart)' : 'not waiting')}`],
          ['Blocked by', n?.blocked_by.length ? n.blocked_by.join(', ') : 'nobody'],
          ['Blocks', n ? `${n.direct_blocked_count} directly, ${n.transitive_blocked_count} in total` : '0'],
          ['Parallel workers', String(s.parallel_workers)],
        ]}
      />
      {s.waiting_locks.length > 0 && (
        <div className="text-sm">
          <p className="font-medium">Waiting for</p>
          <ul className="list-disc pl-5">
            {s.waiting_locks.map((l, i) => (
              <li key={i}>{describeLock(l)}</li>
            ))}
          </ul>
        </div>
      )}
      {s.query !== undefined && (
        <div className="text-sm">
          <p className="font-medium">Query text{s.query_truncated ? ' (truncated)' : ''}</p>
          <pre className="mt-1 overflow-x-auto whitespace-pre-wrap break-all rounded-md bg-panel2 p-2 font-mono text-xs">{s.query}</pre>
        </div>
      )}
      <p className="text-xs text-muted">Lock wait age comes from pg_locks.waitstart only; query_start and state_change are never used as the start of a lock wait.</p>
    </div>
  )
}

function ReportView({ data }: { data: ActivityResponse }) {
  const [selected, setSelected] = useState<number | null>(null)
  const onSelect = useCallback((pid: number) => setSelected(pid), [])
  const report = data.report
  if (!report) return <p className="text-sm text-muted">No blocking snapshot has been collected for this target yet.</p>
  const sum = report.analysis.summary
  const warnings = [...report.snapshot.warnings, ...report.analysis.warnings]
  return (
    <div className="grid gap-4 xl:grid-cols-3">
      <div className="space-y-3 xl:col-span-2">
        <div className="flex flex-wrap gap-2 text-xs">
          <Tag>observed {fmtTime(data.observed_at)}</Tag>
          <Tag>source: {data.source}</Tag>
          <Tag tone={sum.blocked_sessions ? 'crit' : 'muted'}>{sum.blocked_sessions} blocked</Tag>
          <Tag>{sum.root_blockers} root blocker(s)</Tag>
          {sum.cyclic_components > 0 && <Tag tone="warn">{sum.cyclic_components} cycle(s), not confirmed deadlocks</Tag>}
          <Tag>not an atomic snapshot</Tag>
        </div>
        {warnings.map((w, i) => (
          <Notice key={i} tone="warn">
            {w.message}
          </Notice>
        ))}
        <BlockingTree report={report} selected={selected} onSelect={onSelect} />
      </div>
      <Card title={selected === null ? 'Node details' : `Details: ${selected === 0 ? 'prepared transaction' : `pid ${selected}`}`}>
        {selected === null ? <p className="text-sm text-muted">Select a session in the tree.</p> : <NodeDetails report={report} pid={selected} />}
      </Card>
    </div>
  )
}

export function BlockingPage() {
  const id = useTargetId()
  const [params, setParams] = useSearchParams()
  const [range, setRange] = useTimeRange('6h')
  const snapshotId = params.get('snapshot') ? Number(params.get('snapshot')) : null
  const activity = useActivity(id, snapshotId)
  const list = useSnapshotList(id, range.params)
  const [upload, setUpload] = useState<{ data?: ActivityResponse; error?: unknown } | null>(null)
  const pick = (sid: number | null) =>
    setParams(
      (p) => {
        const n = new URLSearchParams(p)
        if (sid === null) n.delete('snapshot')
        else n.set('snapshot', String(sid))
        return n
      },
      { replace: true },
    )
  const onFile = async (f: File | undefined) => {
    if (!f) return
    if (f.size > 10 * 1024 * 1024) {
      setUpload({ error: new Error('The snapshot file exceeds 10 MiB.') })
      return
    }
    try {
      const data = await api<ActivityResponse>('/api/v1/snapshots/analyze', { method: 'POST', body: await f.text(), headers: { 'Content-Type': 'application/json' } })
      setUpload({ data })
    } catch (e) {
      setUpload({ error: e })
    }
  }
  return (
    <>
      <PageHeader
        title="Blocking explorer"
        subtitle="Edges come from pg_blocking_pids(): blocker → waiter. Counts are unique sessions."
        actions={<TimeRangePicker range={range} onChange={setRange} />}
      />
      <div className="grid gap-4 lg:grid-cols-[18rem_1fr]">
        <Card title="Snapshots">
          <button type="button" onClick={() => pick(null)} aria-pressed={snapshotId === null} className={`mb-2 w-full rounded-md border px-2 py-1.5 text-left text-sm ${snapshotId === null ? 'border-accent bg-panel2' : 'border-border'}`}>
            Live (latest collection)
          </button>
          <DataState query={list}>
            {(d) =>
              d.snapshots.length === 0 ? (
                <p className="text-xs text-muted">No stored blocking snapshots in this range.</p>
              ) : (
                <ul className="max-h-[28rem] space-y-1 overflow-y-auto" aria-label="Stored blocking snapshots">
                  {d.snapshots.map((s) => (
                    <li key={s.id}>
                      <button type="button" onClick={() => pick(s.id)} aria-pressed={snapshotId === s.id} className={`w-full rounded-md border px-2 py-1 text-left text-xs ${snapshotId === s.id ? 'border-accent bg-panel2' : 'border-transparent hover:bg-panel2'}`}>
                        {fmtTime(s.at)} · {s.blocked_sessions} blocked · {s.root_blockers} root
                      </button>
                    </li>
                  ))}
                </ul>
              )
            }
          </DataState>
          <div className="mt-4 border-t border-border pt-3 text-sm">
            <label className="block">
              <span className="text-muted">Open a CLI snapshot file (offline analysis)</span>
              <input type="file" accept="application/json,.json" className="mt-1 block w-full text-xs" onChange={(e) => void onFile(e.target.files?.[0])} />
            </label>
          </div>
        </Card>
        <div>
          {upload ? (
            <Card title="Uploaded snapshot" actions={<button type="button" className="text-sm text-accent underline" onClick={() => setUpload(null)}>Close</button>}>
              {upload.error ? <ErrorBox error={upload.error} title="Snapshot rejected" /> : upload.data && <ReportView data={upload.data} />}
            </Card>
          ) : (
            <DataState query={activity} loadingLabel="Loading blocking state">
              {(d) => <ReportView data={d} />}
            </DataState>
          )}
        </div>
      </div>
    </>
  )
}
