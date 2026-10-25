import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router'
import { Database, FileText, Search, TriangleAlert } from 'lucide-react'
import { useFleet, useFleetQueries, useIncidents } from '../api/hooks'
import type { Permission } from '../api/types'
import { useCan } from '../auth'
import { useScope } from '../lib/scope'
import { cx } from './ui'

const PAGES: [string, string, Permission?][] = [
  ['Overview', '/overview'],
  ['Databases', '/databases'],
  ['Topology', '/topology'],
  ['Queries', '/queries'],
  ['Sessions', '/sessions'],
  ['Blocking', '/blocking'],
  ['Explorer', '/explorer'],
  ['SQL Editor', '/sql', 'workspace.sql'],
  ['Incidents', '/incidents'],
  ['Connections', '/connections'],
  ['Settings', '/settings', 'targets.manage'],
  ['Account', '/account'],
]

interface Item {
  key: string
  group: string
  label: string
  detail?: string
  to: string
  icon: 'page' | 'db' | 'incident' | 'query'
}

/** ⌘K palette: pages, instances, open incidents, and statements across instances. */
export function CommandPalette({ open, onClose }: { open: boolean; onClose: () => void }) {
  if (!open) return null
  return <Palette onClose={onClose} />
}

function Palette({ onClose }: { onClose: () => void }) {
  const navigate = useNavigate()
  const can = useCan()
  const { env, range } = useScope()
  const [term, setTerm] = useState('')
  const [active, setActive] = useState(0)
  const input = useRef<HTMLInputElement>(null)
  const fleet = useFleet()
  const incidents = useIncidents({ state: 'open' })
  const t = term.trim().toLowerCase()
  const queries = useFleetQueries(env, range, 'total_time', 8, t.length >= 3 ? term.trim() : '')

  useEffect(() => input.current?.focus(), [])

  const items = useMemo<Item[]>(() => {
    const out: Item[] = []
    const match = (s: string) => !t || s.toLowerCase().includes(t)
    for (const [label, to, perm] of PAGES) if (match(label) && (!perm || can(perm))) out.push({ key: `p${to}`, group: 'Pages', label, to, icon: 'page' })
    for (const e of fleet.data?.targets ?? []) {
      const tg = e.target
      if (match(`${tg.name} ${tg.host} ${tg.database} ${tg.environment}`)) out.push({ key: `t${tg.id}`, group: 'Databases', label: tg.name, detail: `${tg.host}:${tg.port}/${tg.database} · ${tg.status.state}`, to: `/databases/${tg.id}`, icon: 'db' })
    }
    for (const i of incidents.data?.incidents ?? []) if (match(`${i.title} ${i.target_name}`)) out.push({ key: `i${i.id}`, group: 'Open incidents', label: i.title, detail: i.target_name, to: `/incidents/${i.id}`, icon: 'incident' })
    if (t.length >= 3) {
      for (const q of queries.data?.items ?? []) out.push({ key: `q${q.target_id}-${q.identity_id}`, group: 'Queries', label: q.query_text ?? `queryid ${q.queryid}`, detail: `${q.target_name} · ${q.datname}`, to: `/queries/${q.target_id}/${q.identity_id}`, icon: 'query' })
    }
    return out.slice(0, 40)
  }, [t, fleet.data, incidents.data, queries.data, can])

  const go = (it: Item | undefined) => {
    if (!it) return
    onClose()
    navigate(it.to)
  }
  const Icon = { page: FileText, db: Database, incident: TriangleAlert, query: Search }

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center bg-black/40 p-4 pt-[12vh]" onClick={onClose}>
      <div role="dialog" aria-modal="true" aria-label="Search" className="w-full max-w-xl overflow-hidden rounded-2xl border border-line bg-surface shadow-2xl" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center gap-2 border-b border-line px-4">
          <Search aria-hidden className="h-4 w-4 text-muted" />
          <input
            ref={input}
            role="combobox"
            aria-expanded="true"
            aria-controls="palette-results"
            aria-label="Search databases, queries, incidents, pages"
            placeholder="Search databases, queries, incidents, pages…"
            className="h-12 flex-1 bg-transparent text-sm text-ink outline-none placeholder:text-muted"
            value={term}
            onChange={(e) => {
              setTerm(e.target.value)
              setActive(0)
            }}
            onKeyDown={(e) => {
              if (e.key === 'Escape') onClose()
              else if (e.key === 'ArrowDown') {
                e.preventDefault()
                setActive((a) => Math.min(items.length - 1, a + 1))
              } else if (e.key === 'ArrowUp') {
                e.preventDefault()
                setActive((a) => Math.max(0, a - 1))
              } else if (e.key === 'Enter') go(items[active])
            }}
          />
          <kbd className="rounded border border-line-strong px-1.5 text-[11px] text-ink-2">Esc</kbd>
        </div>
        <ul id="palette-results" role="listbox" aria-label="Results" className="max-h-[55vh] overflow-y-auto p-2">
          {items.length === 0 && <li className="px-3 py-6 text-center text-sm text-muted">{t.length > 0 && t.length < 3 ? 'Type at least 3 characters to search queries.' : 'No results.'}</li>}
          {items.map((it, i) => {
            const I = Icon[it.icon]
            const first = i === 0 || items[i - 1]?.group !== it.group
            return (
              <li key={it.key} role="presentation">
                {first && <div className="px-3 pt-2 pb-1 text-[11px] font-semibold tracking-wide text-muted uppercase">{it.group}</div>}
                <button
                  type="button"
                  role="option"
                  aria-selected={i === active}
                  onMouseEnter={() => setActive(i)}
                  onClick={() => go(it)}
                  className={cx('flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left text-sm', i === active ? 'bg-accent-soft text-ink' : 'text-ink-2')}
                >
                  <I aria-hidden className="h-4 w-4 shrink-0 text-muted" />
                  <span className={cx('min-w-0 flex-1 truncate', it.icon === 'query' && 'font-mono text-[12.5px]')}>{it.label}</span>
                  {it.detail && <span className="shrink-0 truncate text-xs text-muted">{it.detail}</span>}
                </button>
              </li>
            )
          })}
        </ul>
      </div>
    </div>
  )
}
