import type { FleetMetricsResponse, FleetLiveTarget, Replication } from '../api/types'

type Pt = [number, number | null]

/** Points of one fleet metric; a missing bucket breaks the line. */
export function fleetPoints(data: FleetMetricsResponse, metric: string): Pt[] {
  const s = data.series.find((x) => x.metric === metric)
  if (!s) return []
  const step = data.step_seconds * 1000
  const out: Pt[] = []
  let prev: number | null = null
  for (const p of s.points) {
    const t = Date.parse(p.t)
    if (prev !== null && t - prev > 1.5 * step) out.push([prev + step, null])
    out.push([t, p.v])
    prev = t
  }
  return out
}

/**
 * Combines several fleet metrics per bucket with `fn`. A bucket is unknown
 * when any input is unknown there, so sums never silently drop a part.
 */
export function combine(data: FleetMetricsResponse, metrics: string[], fn: (vals: number[]) => number | null): Pt[] {
  const maps = metrics.map((m) => new Map(fleetPoints(data, m).map(([t, v]) => [t, v])))
  const times = [...new Set(maps.flatMap((m) => [...m.keys()]))].sort((a, b) => a - b)
  return times.map((t) => {
    const vals = maps.map((m) => m.get(t))
    if (vals.some((v) => v === null || v === undefined)) return [t, null] as Pt
    return [t, fn(vals as number[])] as Pt
  })
}

/** The first and last known values of a series (for trend labels). */
export function firstLast(points: Pt[]): [number | null, number | null] {
  const known = points.filter((p): p is [number, number] => p[1] !== null)
  if (known.length < 2) return [null, null]
  return [known[0]?.[1] ?? null, known[known.length - 1]?.[1] ?? null]
}

/** Sum of a latest value across fleet targets, or null when nobody reports it. */
export function sumLatest(targets: FleetLiveTarget[], metric: string): number | null {
  let sum = 0
  let any = false
  for (const t of targets) {
    const v = t.latest[metric]?.value
    if (v !== null && v !== undefined) {
      sum += v
      any = true
    }
  }
  return any ? sum : null
}

export interface TopologyEdge {
  primary: FleetLiveTarget
  /** The standby target, when it is monitored too. */
  standby?: FleetLiveTarget
  name: string
  address: string | null
  state: string | null
  lagBytes: number | null
  lagSeconds: number | null
}

export interface TopologyGroup {
  primary: FleetLiveTarget
  edges: TopologyEdge[]
}

function hostOf(r: Replication | null): string | null {
  return r?.standby?.sender_host ?? null
}

/**
 * Replication groups observed by monitored primaries. A standby target is
 * linked to a primary when its WAL receiver's sender host is that primary's
 * host (an observation, not a configured mapping); unmatched senders keep
 * their application name.
 */
export function topology(targets: FleetLiveTarget[]): { groups: TopologyGroup[]; standalone: FleetLiveTarget[] } {
  const primaries = targets.filter((t) => t.replication?.role === 'primary')
  const standbys = targets.filter((t) => t.replication?.role === 'standby')
  const used = new Set<number>()
  const groups: TopologyGroup[] = []
  for (const p of primaries) {
    const edges: TopologyEdge[] = []
    const matched = standbys.filter((s) => hostOf(s.replication) === p.target.host)
    for (const r of p.replication?.replicas ?? []) {
      const sb = matched.find((s) => !used.has(s.target.id))
      if (sb) used.add(sb.target.id)
      edges.push({ primary: p, standby: sb, name: sb?.target.name ?? r.application_name ?? `pid ${r.pid}`, address: r.client_addr, state: r.state, lagBytes: r.replay_lag_bytes, lagSeconds: r.replay_lag_seconds })
    }
    groups.push({ primary: p, edges })
  }
  const standalone = targets.filter((t) => !primaries.includes(t) && !used.has(t.target.id))
  return { groups, standalone }
}
