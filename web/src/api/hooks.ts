import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, qs } from './client'
import type {
  ActivityResponse,
  CommandsResponse,
  FindingRow,
  FleetEntry,
  FleetLiveResponse,
  FleetMetricsResponse,
  FleetQueriesResponse,
  SystemResponse,
  GridResult,
  Incident,
  IncidentEventsResponse,
  StoreTable,
  StoreTimescale,
  ChannelInput,
  Delivery,
  NotificationChannel,
  Silence,
  LoadDimension,
  LoadResponse,
  MetricsResponse,
  OverviewResponse,
  QueriesResponse,
  QueryDetailResponse,
  Replication,
  Report,
  Role,
  Session,
  SetupPlan,
  TablesResponse,
  User,
  Target,
  TargetInput,
  TestResult,
  Thresholds,
  WorkspacePolicy,
  IncidentInvestigationResponse,
  PerformanceResponse,
  PlanCompareResponse,
  PlanDetailResponse,
  PlanRow,
  QueryInsightResponse,
} from './types'
import { liveState, rangeQuery, type Range } from '../lib/scope'

// Query keys: ['t', targetId, 'live' | 'hist', ...]. The event stream
// invalidates a target's 'live' keys after each collection; history keys
// refresh on a slower interval.

const LIVE = 15_000
const HIST = 30_000

/** A refetch interval that pauses while the Live switch is off. */
const every = (ms: number) => () => (liveState.on ? ms : false)

export function useFleet() {
  return useQuery({ queryKey: ['fleet'], queryFn: () => api<{ targets: FleetEntry[]; generated_at: string }>('/api/v1/fleet'), refetchInterval: every(LIVE) })
}

export function useTargets() {
  return useQuery({ queryKey: ['targets'], queryFn: () => api<{ targets: Target[] }>('/api/v1/targets') })
}

export function useTarget(id: number) {
  return useQuery({ queryKey: ['t', id, 'live', 'target'], queryFn: () => api<Target>(`/api/v1/targets/${id}`), enabled: id > 0, refetchInterval: every(LIVE) })
}

export function useOverview(id: number) {
  return useQuery({ queryKey: ['t', id, 'live', 'overview'], queryFn: () => api<OverviewResponse>(`/api/v1/targets/${id}/overview`), refetchInterval: every(LIVE) })
}

export function useActivity(id: number) {
  return useQuery({ queryKey: ['t', id, 'live', 'activity'], queryFn: () => api<ActivityResponse>(`/api/v1/targets/${id}/activity`), refetchInterval: every(10_000) })
}

export function useTables(id: number) {
  return useQuery({ queryKey: ['t', id, 'live', 'tables'], queryFn: () => api<TablesResponse>(`/api/v1/targets/${id}/tables`), refetchInterval: every(60_000) })
}

export function useReplication(id: number) {
  return useQuery({
    queryKey: ['t', id, 'live', 'replication'],
    queryFn: () => api<{ source: string; replication: Replication | null; observed_at?: string }>(`/api/v1/targets/${id}/replication`),
    refetchInterval: every(LIVE),
  })
}

export function useFindings(id: number, status: 'current' | 'resolved' = 'current') {
  return useQuery({ queryKey: ['t', id, 'live', 'findings', status], queryFn: () => api<{ findings: FindingRow[] }>(`/api/v1/targets/${id}/findings${qs({ status })}`) })
}

export function useMetrics(id: number, names: string[], range: Range) {
  return useQuery({
    queryKey: ['t', id, 'hist', 'metrics', names.join(','), range],
    queryFn: () => api<MetricsResponse>(`/api/v1/targets/${id}/metrics${qs({ names: names.join(','), ...rangeQuery(range) })}`),
    placeholderData: keepPreviousData,
    refetchInterval: every(HIST),
  })
}

export function useCommands(id: number, range: Range, includeMonitoring = false) {
  return useQuery({
    queryKey: ['t', id, 'hist', 'commands', range, includeMonitoring],
    queryFn: () => api<CommandsResponse>(`/api/v1/targets/${id}/queries/commands${qs({ ...rangeQuery(range), include_monitoring: includeMonitoring || undefined })}`),
    placeholderData: keepPreviousData,
    refetchInterval: every(HIST),
  })
}

export interface QueryParams {
  range: Range
  sort: string
  order: 'asc' | 'desc'
  page: number
  q: string
  command: string
  compare: boolean
  includeMonitoring: boolean
  pageSize?: number
}

export function useQueries(id: number, p: QueryParams) {
  return useQuery({
    queryKey: ['t', id, 'hist', 'queries', p],
    queryFn: () =>
      api<QueriesResponse>(
        `/api/v1/targets/${id}/queries${qs({ ...rangeQuery(p.range), sort: p.sort, order: p.order, page: p.page, page_size: p.pageSize ?? 25, q: p.q, command: p.command, compare: p.compare || undefined, include_monitoring: p.includeMonitoring || undefined })}`,
      ),
    placeholderData: keepPreviousData,
    refetchInterval: every(HIST),
  })
}

export function useLoad(id: number, range: Range, by: LoadDimension, filter: { class?: string; queryid?: string } = {}, enabled = true) {
  return useQuery({
    queryKey: ['t', id, 'hist', 'load', range, by, filter.class ?? '', filter.queryid ?? ''],
    queryFn: () => api<LoadResponse>(`/api/v1/targets/${id}/load${qs({ ...rangeQuery(range), by, class: filter.class, queryid: filter.queryid })}`),
    placeholderData: keepPreviousData,
    refetchInterval: every(HIST),
    enabled: enabled && id > 0,
  })
}

export function useQueryDetail(id: number, qid: number, range: Range) {
  return useQuery({
    queryKey: ['t', id, 'hist', 'query', qid, range],
    queryFn: () => api<QueryDetailResponse>(`/api/v1/targets/${id}/queries/${qid}${qs({ ...rangeQuery(range) })}`),
    placeholderData: keepPreviousData,
    refetchInterval: every(HIST),
  })
}

export function useIncidents(params: { state?: string; target?: number; page?: number }) {
  return useQuery({
    queryKey: ['incidents', params],
    queryFn: () => api<{ incidents: Incident[]; total: number; page_size: number; rules: string[] }>(`/api/v1/incidents${qs({ state: params.state, target: params.target, page: params.page })}`),
    refetchInterval: every(LIVE),
  })
}

export function useIncident(id: number) {
  return useQuery({ queryKey: ['incidents', 'one', id], queryFn: () => api<{ incident: Incident; findings: FindingRow[] }>(`/api/v1/incidents/${id}`) })
}

export function useIncidentEvents(id: number) {
  return useQuery({ queryKey: ['incidents', 'events', id], queryFn: () => api<IncidentEventsResponse>(`/api/v1/incidents/${id}/events?limit=2000`) })
}

/** The blocking analysis of a snapshot stored in an incident event (evidence never changes, so it is cached). */
export function useEventSnapshotReport(eventId: number | undefined, snapshot: unknown) {
  return useQuery({
    queryKey: ['incidents', 'snapshot-report', eventId],
    queryFn: () => api<{ report: Report }>('/api/v1/snapshots/analyze', { method: 'POST', json: snapshot }).then((r) => r.report),
    enabled: eventId !== undefined && !!snapshot,
    staleTime: Infinity,
  })
}

export function useAcknowledge(id: number) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (note: string) => api<Incident>(`/api/v1/incidents/${id}/acknowledge`, { method: 'POST', json: { note } }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['incidents'] }),
  })
}

export function useSaveTarget(id?: number) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: TargetInput) =>
      api<Target>(id ? `/api/v1/targets/${id}` : '/api/v1/targets', { method: id ? 'PUT' : 'POST', json: { ...input, dsn: input.dsn || undefined } }),
    onSuccess: () => void qc.invalidateQueries(),
  })
}

export function useDeleteTarget() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, purge }: { id: number; purge: boolean }) => api<undefined>(`/api/v1/targets/${id}${qs({ purge: purge || undefined })}`, { method: 'DELETE' }),
    onSuccess: () => void qc.invalidateQueries(),
  })
}

export function useTestConnection() {
  return useMutation({
    mutationFn: ({ id, dsn }: { id?: number; dsn?: string }) =>
      id && !dsn ? api<TestResult>(`/api/v1/targets/${id}/test`, { method: 'POST' }) : api<TestResult>('/api/v1/targets/test', { method: 'POST', json: { dsn } }),
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

export function useRuleSettings() {
  return useQuery({ queryKey: ['rule-settings'], queryFn: () => api<{ thresholds: Thresholds; defaults: Thresholds }>('/api/v1/settings/rules') })
}

export function useSaveRules() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (t: Thresholds) => api<unknown>('/api/v1/settings/rules', { method: 'PUT', json: t }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['rule-settings'] }),
  })
}

export function useStorage() {
  return useQuery({ queryKey: ['storage'], queryFn: () => api<{ tables: StoreTable[] | null; timescale?: StoreTimescale }>('/api/v1/storage') })
}

// Workspace: catalog metadata and table data, run on demand against the
// target with its separate, least-privileged role.

export function usePolicy(id: number) {
  return useQuery({ queryKey: ['t', id, 'policy'], queryFn: () => api<WorkspacePolicy>(`/api/v1/targets/${id}/workspace/policy`) })
}

export function useSavePolicy(id: number) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (p: { metadata_enabled: boolean; data_enabled: boolean; sql_enabled?: boolean; dsn?: string }) =>
      api<WorkspacePolicy>(`/api/v1/targets/${id}/workspace/policy`, { method: 'PUT', json: { sql_enabled: false, ...p, history_enabled: false } }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['t', id] }),
  })
}

// The server runs at most two workspace operations per target at a time and
// answers 429 beyond that; a page opening several catalog views at once
// queues them here instead.
const WORKSPACE_SLOTS = 2
const running = new Map<number, number>()
const waiting = new Map<number, (() => void)[]>()

async function acquire(id: number): Promise<void> {
  if ((running.get(id) ?? 0) < WORKSPACE_SLOTS) {
    running.set(id, (running.get(id) ?? 0) + 1)
    return
  }
  await new Promise<void>((resolve) => waiting.set(id, [...(waiting.get(id) ?? []), resolve]))
}

function release(id: number): void {
  const next = waiting.get(id)?.shift()
  if (next) next()
  else running.set(id, Math.max(0, (running.get(id) ?? 1) - 1))
}

/** Retries only "busy" (another browser tab may be using the slots). */
function retryBusy(n: number, e: Error): boolean {
  return e instanceof ApiError && e.status === 429 && n < 3
}

export async function runWorkspace<T>(id: number, input: Record<string, unknown>, signal?: AbortSignal): Promise<T> {
  await acquire(id)
  try {
    signal?.throwIfAborted()
    return await api<T>(`/api/v1/targets/${id}/workspace/run`, { method: 'POST', json: { request_id: crypto.randomUUID(), ...input }, signal })
  } finally {
    release(id)
  }
}

export function useCatalog<T = GridResult>(id: number, enabled: boolean, kind: string, schema = '', relation = '') {
  return useQuery({
    queryKey: ['t', id, 'catalog', kind, schema, relation],
    queryFn: ({ signal }) => runWorkspace<T>(id, { action: 'metadata', kind, schema, relation }, signal),
    enabled,
    staleTime: 30_000,
    retry: retryBusy,
    retryDelay: (n) => 500 * 2 ** n,
  })
}

export interface DataRequest {
  schema: string
  relation: string
  sort?: string
  desc?: boolean
  filter_column?: string
  filter_op?: string
  filter_value?: string
  limit: number
  offset: number
}

export function useTableData(id: number, enabled: boolean, req: DataRequest) {
  return useQuery({
    queryKey: ['t', id, 'data', req],
    queryFn: ({ signal }) => runWorkspace<GridResult>(id, { action: 'data', data: req }, signal),
    enabled,
    placeholderData: keepPreviousData,
    staleTime: 15_000,
    retry: retryBusy,
    retryDelay: (n) => 500 * 2 ** n,
  })
}

// Fleet views: all enabled targets of one environment ('' = all).

export function useFleetMetrics(env: string, names: string[], range: Range) {
  return useQuery({
    queryKey: ['fleet', 'metrics', env, names.join(','), range],
    queryFn: () => api<FleetMetricsResponse>(`/api/v1/fleet/metrics${qs({ names: names.join(','), ...rangeQuery(range), environment: env })}`),
    placeholderData: keepPreviousData,
    refetchInterval: every(HIST),
  })
}

export function useFleetQueries(env: string, range: Range, sort: string, limit = 10, q = '', command = '') {
  return useQuery({
    queryKey: ['fleet', 'queries', env, range, sort, limit, q, command],
    queryFn: () => api<FleetQueriesResponse>(`/api/v1/fleet/queries${qs({ ...rangeQuery(range), sort, limit, environment: env, q, command })}`),
    placeholderData: keepPreviousData,
    refetchInterval: every(HIST),
  })
}

export function useFleetLive(env: string) {
  return useQuery({
    queryKey: ['fleet', 'live', env],
    queryFn: () => api<FleetLiveResponse>(`/api/v1/fleet/live${qs({ environment: env })}`),
    placeholderData: keepPreviousData,
    refetchInterval: every(10_000),
  })
}

export function useSystem() {
  return useQuery({ queryKey: ['system'], queryFn: () => api<SystemResponse>('/api/v1/system'), refetchInterval: every(LIVE) })
}

export function useExplain(id: number) {
  return useMutation({
    mutationFn: ({ sql, schema, signal }: { sql: string; schema: string; signal?: AbortSignal }) => runWorkspace<GridResult>(id, { action: 'explain', sql, schema }, signal),
  })
}

export function useRunSql(id: number) {
  return useMutation({
    mutationFn: ({ sql, schema, requestId, signal }: { sql: string; schema: string; requestId: string; signal?: AbortSignal }) =>
      api<GridResult>(`/api/v1/targets/${id}/workspace/run`, { method: 'POST', json: { request_id: requestId, action: 'sql', sql, schema }, signal }),
  })
}

export function cancelSql(id: number, requestId: string) {
  return api<{ cancelled: boolean }>(`/api/v1/targets/${id}/workspace/cancel`, { method: 'POST', json: { request_id: requestId } })
}

// Users (admin) and the signed-in user's own password.

export function useUsers() {
  return useQuery({ queryKey: ['users'], queryFn: () => api<{ users: User[]; roles: Role[] }>('/api/v1/users') })
}

export function useCreateUser() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (u: { username: string; role: Role; password: string }) => api<User>('/api/v1/users', { method: 'POST', json: u }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['users'] }),
  })
}

export function useUpdateUser() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...c }: { id: number; role?: Role; disabled?: boolean; password?: string }) => api<User>(`/api/v1/users/${id}`, { method: 'PUT', json: c }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['users'] }),
  })
}

export function useDeleteUser() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api<undefined>(`/api/v1/users/${id}`, { method: 'DELETE' }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['users'] }),
  })
}

export function useChangePassword() {
  return useMutation({
    mutationFn: (b: { current_password: string; new_password: string }) => api<Session>('/api/v1/session/password', { method: 'PUT', json: b }),
  })
}

// Adding an instance from a connection string.

export function useInspectConnection() {
  return useMutation({ mutationFn: (dsn: string) => api<SetupPlan>('/api/v1/targets/inspect', { method: 'POST', json: { dsn } }) })
}

export interface QuickAddRequest {
  dsn: string
  name: string
  environment: string
  browse_data: boolean
  run_sql: boolean
  confirm: boolean
}

export function useQuickAdd() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (r: QuickAddRequest) => api<{ target: Target; mode: string; warnings: string[] }>('/api/v1/targets/quick', { method: 'POST', json: r }),
    onSuccess: () => void qc.invalidateQueries(),
  })
}


export function useChannels(enabled = true) {
  return useQuery({ queryKey: ['notify', 'channels'], queryFn: () => api<{ channels: NotificationChannel[] }>('/api/v1/notifications/channels'), enabled })
}

export function useSaveChannel() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, input }: { id?: number; input: ChannelInput }) =>
      api<NotificationChannel>(id ? `/api/v1/notifications/channels/${id}` : '/api/v1/notifications/channels', { method: id ? 'PUT' : 'POST', json: input }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['notify'] }),
  })
}

export function useDeleteChannel() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api<undefined>(`/api/v1/notifications/channels/${id}`, { method: 'DELETE' }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['notify'] }),
  })
}

export function useTestChannel() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api<Delivery>(`/api/v1/notifications/channels/${id}/test`, { method: 'POST' }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['notify', 'deliveries'] }),
  })
}

/** Recent deliveries: of one incident, or (administrators) of every channel. */
export function useDeliveries(incident?: number, enabled = true) {
  return useQuery({
    queryKey: ['notify', 'deliveries', incident ?? 'all'],
    queryFn: () => api<{ deliveries: Delivery[] }>(`/api/v1/notifications/deliveries${qs({ incident })}`),
    refetchInterval: every(LIVE),
    enabled,
  })
}

export function useSilences() {
  return useQuery({ queryKey: ['notify', 'silences'], queryFn: () => api<{ silences: Silence[] }>('/api/v1/notifications/silences'), refetchInterval: every(LIVE) })
}

export function useCreateSilence() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (x: { target_id: number | null; rule_id: string | null; minutes: number; reason: string }) =>
      api<Silence>('/api/v1/notifications/silences', { method: 'POST', json: x }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['notify'] }),
  })
}

export function useEndSilence() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api<undefined>(`/api/v1/notifications/silences/${id}`, { method: 'DELETE' }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['notify'] }),
  })
}

export function usePerformance(env: string) {
  return useQuery({
    queryKey: ['fleet', 'performance', env],
    queryFn: () => api<PerformanceResponse>(`/api/v1/performance${qs({ environment: env })}`),
    placeholderData: keepPreviousData,
    refetchInterval: every(LIVE),
  })
}

export function useQueryInsight(id: number, qid: number, range: Range) {
  return useQuery({
    queryKey: ['t', id, 'hist', 'query-insight', qid, range],
    queryFn: () => api<QueryInsightResponse>(`/api/v1/targets/${id}/queries/${qid}/insight${qs({ ...rangeQuery(range) })}`),
    placeholderData: keepPreviousData,
    refetchInterval: every(HIST),
  })
}

export function useIncidentInvestigation(id: number, enabled: boolean) {
  return useQuery({
    queryKey: ['incidents', 'investigation', id],
    queryFn: () => api<IncidentInvestigationResponse>(`/api/v1/incidents/${id}/investigation`),
    enabled,
  })
}

export function usePlans(id: number, qid: number) {
  return useQuery({ queryKey: ['t', id, 'plans', qid], queryFn: () => api<{ plans: PlanRow[]; note: string }>(`/api/v1/targets/${id}/queries/${qid}/plans`) })
}

export function usePlan(id: number, pid: number | null) {
  return useQuery({
    queryKey: ['t', id, 'plan', pid],
    queryFn: () => api<PlanDetailResponse>(`/api/v1/targets/${id}/plans/${pid}`),
    enabled: pid !== null,
    staleTime: Infinity,
  })
}

export function usePlanCompare(id: number, a: number | null, b: number | null) {
  return useQuery({
    queryKey: ['t', id, 'plan-compare', a, b],
    queryFn: () => api<PlanCompareResponse>(`/api/v1/targets/${id}/plans/compare${qs({ a, b })}`),
    enabled: a !== null && b !== null && a !== b,
    staleTime: Infinity,
  })
}

export function useSavePlan(id: number, qid: number) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { plan: string; note: string; source?: 'manual' | 'workspace_explain' }) =>
      api<{ plan: PlanRow }>(`/api/v1/targets/${id}/queries/${qid}/plans`, { method: 'POST', json: body }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['t', id] }),
  })
}

export function useCapturePlan(id: number, qid: number) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (note: string) =>
      api<{ plan: PlanRow }>(`/api/v1/targets/${id}/queries/${qid}/plans/capture`, { method: 'POST', json: { request_id: crypto.randomUUID(), note } }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['t', id] }),
  })
}

export function useDeletePlan(id: number) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (pid: number) => api<null>(`/api/v1/targets/${id}/plans/${pid}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['t', id] }),
  })
}
