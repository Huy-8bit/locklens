// Changes (settings, restarts, schema, deploys) are drawn as vertical
// markers on every time chart of a page that provides them.
import { createContext, useContext, useMemo, type ReactNode } from 'react'
import { useChanges } from '../api/hooks'
import type { ChangeEvent, ChangeKind } from '../api/types'
import { useRange } from './scope'

export const CHANGE_KIND: Record<ChangeKind, string> = {
  settings: 'Settings',
  restart: 'Restart',
  promotion: 'Promotion',
  demotion: 'Demotion',
  upgrade: 'Upgrade',
  schema: 'Schema',
  deploy: 'Deploy',
  migration: 'Migration',
  config: 'Config',
  note: 'Note',
}

/** A change drawn on charts. */
export interface ChartMarker {
  at: number
  label: string
}

const Markers = createContext<ChartMarker[]>([])

export function toMarkers(changes: ChangeEvent[]): ChartMarker[] {
  return changes.map((c) => ({ at: Date.parse(c.at), label: `${CHANGE_KIND[c.kind] ?? c.kind}: ${c.title}` }))
}

/** Provides the changes of some instances in the page's range to its charts. */
export function ChangeMarkers({ targetIds, children }: { targetIds: number[]; children: ReactNode }) {
  const [range] = useRange()
  const q = useChanges(targetIds, range)
  const markers = useMemo(() => toMarkers(q.data?.changes ?? []), [q.data])
  return <Markers.Provider value={markers}>{children}</Markers.Provider>
}

export function useChartMarkers(): ChartMarker[] {
  return useContext(Markers)
}
