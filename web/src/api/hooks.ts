import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, qs } from './client'
import type {
  ActivityResponse,
  FindingRow,
  FleetEntry,
  HostResponse,
  Incident,
  IncidentEvent,
  MetricsResponse,
  QueriesResponse,
  QueryDetailResponse,
  Replication,
  Target,
  TargetInput,
  Thresholds,
  TablesResponse,
} from './types'

// Polling intervals are a fallback; server-sent events invalidate queries as
// soon as new data is collected (see lib/events.ts).
const LIVE = 15_000

export function useFleet() {
  return useQuery({ queryKey: ['fleet'], queryFn: () => api<{ targets: FleetEntry[]; generated_at: string }>('/api/v1/fleet'), refetchInterval: LIVE })
}

export function useTargets() {
  return useQuery({ queryKey: ['targets'], queryFn: () => api<{ targets: Target[] }>('/api/v1/targets'), refetchInterval: 30_000 })
}

/** Loads one target. Id 0 means "no target yet" (the new-target form) and is not fetched; an invalid id still errors. */
export function useTarget(id: number) {
  return useQuery({ queryKey: ['target', id], queryFn: () => api<Target>(`/api/v1/targets/${id}`), refetchInterval: LIVE, enabled: id !== 0 })
}

export interface OverviewResponse {
  target: Target
  latest: Record<string, { at: string; value: number | null }>
  connected: boolean
  connect_error?: string | null
  activity_at?: string
  counts?: ActivityResponse['counts']
  summary?: { blocked_sessions: number; root_blockers: number; long_transactions: number; idle_in_transaction: number }
  visibility_warnings?: { code: string; message: string }[]
  host_configured?: boolean
  host_error?: string | null
  findings: FindingRow[]
  open_incidents: Incident[]
  open_incident_count: number
}

export function useOverview(id: number) {
  return useQuery({ queryKey: ['overview', id], queryFn: () => api<OverviewResponse>(`/api/v1/targets/${id}/overview`), refetchInterval: LIVE })
}

export function useMetrics(id: number, names: string[], range: Record<string, string>) {
  return useQuery({
    queryKey: ['metrics', id, names.join(','), range],
    queryFn: () => api<MetricsResponse>(`/api/v1/targets/${id}/metrics${qs({ names: names.join(','), ...range })}`),
    refetchInterval: 30_000,
    placeholderData: keepPreviousData,
  })
}

export function useActivity(id: number, snapshotId?: number | null) {
  return useQuery({
    queryKey: ['activity', id, snapshotId ?? 'live'],
    queryFn: () =>
      api<ActivityResponse>(snapshotId ? `/api/v1/targets/${id}/blocking/snapshots/${snapshotId}` : `/api/v1/targets/${id}/activity`),
    refetchInterval: snapshotId ? false : 10_000,
    placeholderData: keepPreviousData,
  })
}

export function useSnapshotList(id: number, range: Record<string, string>) {
  return useQuery({
    queryKey: ['snapshots', id, range],
    queryFn: () =>
      api<{ snapshots: { id: number; at: string; blocked_sessions: number; root_blockers: number; long_transactions: number }[] }>(
        `/api/v1/targets/${id}/blocking/snapshots${qs({ ...range, blocking: true, limit: 300 })}`,
      ),
    refetchInterval: 30_000,
  })
}

export interface QueryListParams {
  range: Record<string, string>
  sort: string
  order: 'asc' | 'desc'
  q: string
  page: number
  compare: boolean
  includeText: boolean
  includeMonitoring: boolean
}

export function useQueries(id: number, p: QueryListParams) {
  return useQuery({
    queryKey: ['queries', id, p],
    queryFn: () =>
      api<QueriesResponse>(
        `/api/v1/targets/${id}/queries${qs({ ...p.range, sort: p.sort, order: p.order, q: p.q, page: p.page, page_size: 25, compare: p.compare, include_text: p.includeText, include_monitoring: p.includeMonitoring })}`,
      ),
    placeholderData: keepPreviousData,
    refetchInterval: 60_000,
  })
}

export function useQueryDetail(id: number, qid: number, range: Record<string, string>, includeText: boolean) {
  return useQuery({
    queryKey: ['query', id, qid, range, includeText],
    queryFn: () => api<QueryDetailResponse>(`/api/v1/targets/${id}/queries/${qid}${qs({ ...range, include_text: includeText })}`),
    placeholderData: keepPreviousData,
  })
}

export function useTables(id: number) {
  return useQuery({ queryKey: ['tables', id], queryFn: () => api<TablesResponse>(`/api/v1/targets/${id}/tables`), refetchInterval: 60_000 })
}

export function useReplication(id: number) {
  return useQuery({
    queryKey: ['replication', id],
    queryFn: () => api<{ source: string; replication: Replication | null; observed_at?: string }>(`/api/v1/targets/${id}/replication`),
    refetchInterval: LIVE,
  })
}

export function useHost(id: number) {
  return useQuery({ queryKey: ['host', id], queryFn: () => api<HostResponse>(`/api/v1/targets/${id}/host`), refetchInterval: LIVE })
}

export function useFindings(id: number, status: 'current' | 'resolved') {
  return useQuery({
    queryKey: ['findings', id, status],
    queryFn: () => api<{ findings: FindingRow[] }>(`/api/v1/targets/${id}/findings${qs({ status })}`),
    refetchInterval: LIVE,
  })
}

export interface IncidentListParams { state: string; target: string; rule: string; page: number }

export function useIncidents(p: IncidentListParams) {
  return useQuery({
    queryKey: ['incidents', p],
    queryFn: () =>
      api<{ incidents: Incident[]; total: number; page_size: number; rules: string[] }>(
        `/api/v1/incidents${qs({ state: p.state, target: p.target, rule: p.rule, page: p.page, page_size: 25 })}`,
      ),
    placeholderData: keepPreviousData,
    refetchInterval: LIVE,
  })
}

export function useIncident(id: number) {
  return useQuery({
    queryKey: ['incident', id],
    queryFn: () => api<{ incident: Incident; findings: FindingRow[] }>(`/api/v1/incidents/${id}`),
    refetchInterval: LIVE,
  })
}

export function useIncidentEvents(id: number) {
  return useQuery({
    queryKey: ['incident-events', id],
    queryFn: () => api<{ events: IncidentEvent[]; has_more: boolean }>(`/api/v1/incidents/${id}/events?limit=2000`),
    refetchInterval: LIVE,
  })
}

export function useAcknowledge(id: number) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (note: string) => api<Incident>(`/api/v1/incidents/${id}/acknowledge`, { method: 'POST', json: { note } }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['incident', id] })
      void qc.invalidateQueries({ queryKey: ['incidents'] })
    },
  })
}

export function useImportIncident() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (f: File) => {
      if (f.size > 10 * 1024 * 1024) throw new Error('The incident file exceeds 10 MiB.')
      return api<{ id: number }>('/api/v1/incidents/import', { method: 'POST', body: await f.text(), headers: { 'Content-Type': 'application/json' } })
    },
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['incidents'] }),
  })
}

export interface StorageTable {
  table: string
  bytes: number
  estimated_rows: number
}

export function useStorageStats() {
  return useQuery({ queryKey: ['storage'], queryFn: () => api<{ tables: StorageTable[] | null }>('/api/v1/storage'), refetchInterval: 5 * 60_000 })
}

export function useRuleSettings() {
  return useQuery({
    queryKey: ['rule-settings'],
    queryFn: () =>
      api<{ thresholds: Thresholds; defaults: Thresholds; rule_versions: Record<string, number>; trigger_rules: string[] }>('/api/v1/settings/rules'),
  })
}

export function useSaveRuleSettings() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (th: Thresholds) => api<{ thresholds: Thresholds }>('/api/v1/settings/rules', { method: 'PUT', json: th }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['rule-settings'] }),
  })
}

export function useSaveTarget(id?: number) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: TargetInput) =>
      id ? api<Target>(`/api/v1/targets/${id}`, { method: 'PUT', json: input }) : api<Target>('/api/v1/targets', { method: 'POST', json: input }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['targets'] })
      void qc.invalidateQueries({ queryKey: ['fleet'] })
      if (id) void qc.invalidateQueries({ queryKey: ['target', id] })
    },
  })
}

export function useDeleteTarget() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, purge, keepIncidents }: { id: number; purge: boolean; keepIncidents: boolean }) =>
      api<undefined>(`/api/v1/targets/${id}${qs({ purge, keep_incidents: keepIncidents })}`, { method: 'DELETE' }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['targets'] })
      void qc.invalidateQueries({ queryKey: ['fleet'] })
    },
  })
}

export interface TestResult {
  ok: boolean
  duration_ms: number
  target: string
  capabilities: Target['status']['capabilities']
  checks: { name: string; status: 'ok' | 'warn' | 'fail' | 'skip'; detail: string; hint?: string }[]
  error: string | null
  hint: string | null
}

export function useTestConnection() {
  return useMutation({
    mutationFn: ({ id, dsn }: { id?: number; dsn?: string }) =>
      id && !dsn ? api<TestResult>(`/api/v1/targets/${id}/test`, { method: 'POST' }) : api<TestResult>('/api/v1/targets/test', { method: 'POST', json: { dsn } }),
  })
}
