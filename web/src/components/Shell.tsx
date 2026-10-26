import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Link, NavLink, Outlet, useLocation, useParams } from 'react-router'
import {
  Bell,
  Cable,
  ChevronDown,
  CircleUser,
  CircleCheck,
  Database,
  FolderTree,
  LayoutDashboard,
  Lock,
  LogOut,
  Menu,
  Moon,
  Network,
  OctagonAlert,
  ScrollText,
  Search,
  Settings,
  SquareTerminal,
  Sun,
  TriangleAlert,
  Users,
  X,
} from 'lucide-react'
import { useFleet, useIncidents, useSystem } from '../api/hooks'
import type { SystemResponse } from '../api/types'
import { ROLE_LABEL, useAuth, useCan } from '../auth'
import { useEventStream } from '../lib/events'
import { useScope } from '../lib/scope'
import { useTheme } from '../lib/theme'
import { CommandPalette } from './CommandPalette'
import { TimeRangePicker } from './TimeRangePicker'
import { cx, SeverityBadge } from './ui'

/** Numeric route parameter, or 0. */
export function useNumParam(name: string): number {
  const v = Number(useParams()[name])
  return Number.isInteger(v) && v > 0 ? v : 0
}

function NavItem({ to, icon, children, badge, end }: { to: string; icon: ReactNode; children: ReactNode; badge?: ReactNode; end?: boolean }) {
  return (
    <NavLink
      to={to}
      end={end}
      className={({ isActive }) =>
        cx('flex items-center gap-3 rounded-lg px-3 py-[7px] text-[13.5px] font-medium transition-colors', isActive ? 'bg-nav-active text-white shadow-sm' : 'text-nav-muted hover:bg-nav-hover hover:text-nav-ink')
      }
    >
      <span aria-hidden className="shrink-0 [&>svg]:h-[18px] [&>svg]:w-[18px]">{icon}</span>
      <span className="min-w-0 flex-1 truncate">{children}</span>
      {badge}
    </NavLink>
  )
}

function statusRows(s: SystemResponse | undefined): { label: string; ok: boolean; detail: string }[] {
  if (!s) return []
  const unreachable = s.targets.by_state.unreachable ?? 0
  const degraded = s.targets.by_state.degraded ?? 0
  return [
    { label: 'Monitoring', ok: unreachable === 0, detail: unreachable ? `${unreachable} unreachable` : degraded ? `${degraded} degraded` : 'Healthy' },
    { label: 'Collectors', ok: s.collectors.failing === 0, detail: s.collectors.failing ? `${s.collectors.failing} failing` : 'Healthy' },
    { label: 'Alerts', ok: s.incidents.open === 0, detail: s.incidents.open ? `${s.incidents.open} open` : 'Healthy' },
    { label: 'Storage', ok: s.store.reachable && s.writer.dropped_batches === 0, detail: !s.store.reachable ? 'Unreachable' : s.writer.dropped_batches ? `${s.writer.dropped_batches} dropped` : 'Healthy' },
  ]
}

function SystemStatus() {
  const q = useSystem()
  const [open, setOpen] = useState(false)
  const rows = statusRows(q.data).filter((r) => r.label !== 'Alerts')
  const problems = rows.filter((r) => !r.ok)
  const summary = !q.data ? 'Checking status…' : problems.length === 0 ? 'All systems operational' : problems.map((p) => `${p.label}: ${p.detail}`).join(' · ')
  return (
    <section aria-label="System status" className="rounded-lg">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-[12.5px] text-nav-muted hover:bg-nav-hover hover:text-nav-ink"
      >
        <span aria-hidden className={cx('h-2 w-2 shrink-0 rounded-full', !q.data ? 'bg-nav-muted' : problems.length ? 'bg-[#fbbf24]' : 'bg-[#4ade80]')} />
        <span className="min-w-0 flex-1 truncate">{summary}</span>
        <ChevronDown aria-hidden className={cx('h-3.5 w-3.5 shrink-0 transition-transform', open && 'rotate-180')} />
      </button>
      {open && (
        <ul className="mt-1 space-y-1 px-3 pb-1">
          {rows.map((r) => (
            <li key={r.label} className="flex items-center justify-between gap-2 text-[12px]">
              <span className="flex items-center gap-2 text-nav-muted">
                {r.ok ? <CircleCheck aria-hidden className="h-3.5 w-3.5 text-[#4ade80]" /> : <OctagonAlert aria-hidden className="h-3.5 w-3.5 text-[#f87171]" />}
                {r.label}
              </span>
              <span className={r.ok ? 'text-nav-muted' : 'text-[#fca5a5]'}>{r.detail}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

function useClickAway(ref: React.RefObject<HTMLElement | null>, open: boolean, close: () => void) {
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => ref.current && !ref.current.contains(e.target as Node) && close()
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && close()
    window.addEventListener('mousedown', onDown)
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('mousedown', onDown)
      window.removeEventListener('keydown', onKey)
    }
  }, [ref, open, close])
}

function Notifications() {
  const q = useIncidents({ state: 'open' })
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useClickAway(ref, open, () => setOpen(false))
  const items = q.data?.incidents ?? []
  return (
    <div ref={ref} className="relative">
      <button type="button" aria-label={`Notifications: ${items.length} open incident(s)`} aria-expanded={open} onClick={() => setOpen((o) => !o)} className="relative rounded-lg p-2 text-ink-2 hover:bg-surface-2">
        <Bell aria-hidden className="h-5 w-5" />
        {items.length > 0 && <span className="absolute -top-0.5 -right-0.5 min-w-[18px] rounded-full bg-[#dc2626] px-1 text-center text-[11px] font-semibold leading-[18px] text-white">{items.length}</span>}
      </button>
      {open && (
        <div role="dialog" aria-label="Open incidents" className="absolute right-0 z-40 mt-2 w-96 rounded-xl border border-line bg-surface p-2 shadow-xl">
          <p className="px-2 py-1 text-xs font-semibold text-muted">Open incidents</p>
          {items.length === 0 ? (
            <p className="px-2 py-3 text-sm text-muted">Nothing open. All quiet.</p>
          ) : (
            <ul>
              {items.slice(0, 8).map((i) => (
                <li key={i.id}>
                  <Link to={`/incidents/${i.id}`} onClick={() => setOpen(false)} className="flex items-start gap-2 rounded-lg px-2 py-2 text-sm hover:bg-surface-2">
                    <SeverityBadge severity={i.severity} />
                    <span className="min-w-0">
                      <span className="block truncate">{i.title}</span>
                      <span className="text-xs text-muted">{i.target_name}</span>
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          )}
          <Link to="/incidents" onClick={() => setOpen(false)} className="block rounded-lg px-2 py-2 text-sm text-accent hover:bg-surface-2">
            View all incidents
          </Link>
        </div>
      )}
    </div>
  )
}

function UserMenu() {
  const { session, logout } = useAuth()
  const can = useCan()
  const [theme, toggleTheme] = useTheme()
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useClickAway(ref, open, () => setOpen(false))
  const name = session?.username ?? ''
  return (
    <div ref={ref} className="relative">
      <button type="button" aria-expanded={open} onClick={() => setOpen((o) => !o)} className="flex items-center gap-2.5 rounded-lg py-1 pr-1 pl-1 hover:bg-surface-2">
        <span aria-hidden className="flex h-9 w-9 items-center justify-center rounded-full bg-[#1e3a8a] text-sm font-semibold text-white">
          {name.slice(0, 2).toUpperCase()}
        </span>
        <span className="hidden text-left leading-tight lg:block">
          <span className="block text-sm font-semibold text-ink">{name}</span>
          <span className="block text-xs text-muted">{session ? ROLE_LABEL[session.role] : ''}</span>
        </span>
        <ChevronDown aria-hidden className="hidden h-4 w-4 text-muted lg:block" />
        <span className="sr-only">Account menu</span>
      </button>
      {open && (
        <div role="menu" className="absolute right-0 z-40 mt-2 w-52 rounded-xl border border-line bg-surface p-1.5 shadow-xl">
          <button type="button" role="menuitem" onClick={toggleTheme} className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-sm hover:bg-surface-2">
            {theme === 'dark' ? <Sun aria-hidden className="h-4 w-4" /> : <Moon aria-hidden className="h-4 w-4" />}
            Switch to {theme === 'dark' ? 'light' : 'dark'} theme
          </button>
          <Link role="menuitem" to="/account" onClick={() => setOpen(false)} className="flex items-center gap-2 rounded-lg px-2.5 py-2 text-sm hover:bg-surface-2">
            <CircleUser aria-hidden className="h-4 w-4" /> Account
          </Link>
          {can('targets.manage') && (
            <Link role="menuitem" to="/settings" onClick={() => setOpen(false)} className="flex items-center gap-2 rounded-lg px-2.5 py-2 text-sm hover:bg-surface-2">
              <Settings aria-hidden className="h-4 w-4" /> Settings
            </Link>
          )}
          <button type="button" role="menuitem" onClick={() => void logout()} className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-sm text-crit hover:bg-surface-2">
            <LogOut aria-hidden className="h-4 w-4" /> Log out
          </button>
        </div>
      )}
    </div>
  )
}

function LiveSwitch() {
  const { live, setLive } = useScope()
  return (
    <button
      type="button"
      role="switch"
      aria-checked={live}
      onClick={() => setLive(!live)}
      title={live ? 'Pages refresh after every collection. Click to pause.' : 'Paused: nothing refreshes. Click to resume.'}
      className={cx(
        'flex h-9 items-center gap-2 rounded-xl border px-3 text-sm font-medium transition-colors',
        live ? 'border-good/30 bg-good-soft text-good hover:border-good/50' : 'border-line-strong bg-surface text-ink-2 hover:bg-surface-2',
      )}
    >
      <span aria-hidden className={cx('h-2 w-2 rounded-full', live ? 'bg-good-mark motion-safe:animate-pulse' : 'bg-muted')} />
      {live ? 'Live' : 'Paused'}
    </button>
  )
}

export function Shell() {
  const { env, setEnv } = useScope()
  const can = useCan()
  useEventStream(true)
  const fleet = useFleet()
  const incidents = useIncidents({ state: 'open' })
  const location = useLocation()
  const [menuPath, setMenuPath] = useState<string | null>(null)
  const [palette, setPalette] = useState(false)
  const menuOpen = menuPath === location.pathname
  const environments = useMemo(() => [...new Set((fleet.data?.targets ?? []).map((t) => t.target.environment || 'default'))].sort(), [fleet.data])
  const openCount = incidents.data?.total ?? 0

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setPalette(true)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const groups: { label?: string; items: ReactNode[] }[] = [
    {
      items: [
        <NavItem key="o" to="/overview" icon={<LayoutDashboard />}>Overview</NavItem>,
        <NavItem key="d" to="/databases" icon={<Database />}>Databases</NavItem>,
        <NavItem key="t" to="/topology" icon={<Network />}>Topology</NavItem>,
      ],
    },
    {
      label: 'Workload',
      items: [
        <NavItem key="q" to="/queries" icon={<ScrollText />}>Queries</NavItem>,
        <NavItem key="s" to="/sessions" icon={<Users />}>Sessions</NavItem>,
        <NavItem key="b" to="/blocking" icon={<Lock />}>Blocking</NavItem>,
        <NavItem key="c" to="/connections" icon={<Cable />}>Connections</NavItem>,
      ],
    },
    {
      label: 'Investigate',
      items: [
        <NavItem key="e" to="/explorer" icon={<FolderTree />}>Explorer</NavItem>,
        can('workspace.sql') ? <NavItem key="sql" to="/sql" icon={<SquareTerminal />}>SQL Editor</NavItem> : null,
      ],
    },
    {
      label: 'Respond',
      items: [
        <NavItem key="i" to="/incidents" icon={<TriangleAlert />} badge={openCount > 0 ? <span className="rounded-full bg-[#dc2626] px-2 text-[11px] font-semibold leading-5 text-white">{openCount}</span> : undefined}>
          Incidents
        </NavItem>,
      ],
    },
  ]

  const sidebar = (
    <nav aria-label="Main" className="flex h-full flex-col overflow-y-auto bg-nav px-3 py-4">
      <Link to="/" className="mb-4 flex items-center gap-2.5 px-3">
        <Database aria-hidden className="h-7 w-7 text-[#3b82f6]" strokeWidth={2.2} />
        <span className="text-[18px] font-bold tracking-tight text-white">LockLens</span>
      </Link>
      <div className="space-y-4">
        {groups.map((g, i) => (
          <div key={i}>
            {g.label && <p className="px-3 pb-1 text-[11px] font-semibold tracking-wider text-nav-muted uppercase">{g.label}</p>}
            <div className="space-y-0.5">{g.items}</div>
          </div>
        ))}
      </div>
      <div className="mt-auto space-y-1 border-t border-white/10 pt-3">
        {can('targets.manage') && <NavItem to="/settings" icon={<Settings />}>Settings</NavItem>}
        <SystemStatus />
      </div>
    </nav>
  )

  return (
    <div className="flex h-full">
      <a href="#main" className="sr-only focus:not-sr-only focus:fixed focus:z-50 focus:m-2 focus:rounded focus:bg-surface focus:px-3 focus:py-2">
        Skip to content
      </a>
      <aside className="hidden w-64 shrink-0 lg:block">{sidebar}</aside>
      {menuOpen && (
        <div className="fixed inset-0 z-40 flex lg:hidden" onClick={() => setMenuPath(null)}>
          <div className="w-72" onClick={(e) => e.stopPropagation()}>
            {sidebar}
          </div>
          <div className="flex-1 bg-black/40" />
        </div>
      )}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-16 shrink-0 items-center gap-3 border-b border-line bg-surface px-3 lg:px-6">
          <button type="button" className="rounded-md p-2 text-ink-2 hover:bg-surface-2 lg:hidden" aria-label={menuOpen ? 'Close menu' : 'Open menu'} onClick={() => setMenuPath(menuOpen ? null : location.pathname)}>
            {menuOpen ? <X aria-hidden className="h-5 w-5" /> : <Menu aria-hidden className="h-5 w-5" />}
          </button>
          <button
            type="button"
            onClick={() => setPalette(true)}
            className="flex h-9 min-w-0 flex-1 items-center gap-2.5 rounded-xl border border-line bg-surface-2 px-3 text-left text-sm text-muted hover:border-line-strong md:max-w-xs xl:max-w-sm"
          >
            <Search aria-hidden className="h-4 w-4 shrink-0" />
            <span className="truncate">Search or jump to…</span>
            <kbd className="ml-auto hidden rounded border border-line-strong bg-surface px-1.5 font-sans text-[11px] text-ink-2 sm:inline">⌘ K</kbd>
          </button>
          <div className="ml-auto flex items-center gap-1.5 lg:gap-2">
            <label className="hidden h-9 items-center gap-2 rounded-xl border border-line bg-surface px-3 text-sm font-medium md:flex">
              <Database aria-hidden className="h-4 w-4 text-[#16a34a]" />
              <span className="sr-only">Environment</span>
              <select value={env} onChange={(e) => setEnv(e.target.value)} className="bg-transparent pr-1 text-sm font-medium text-ink">
                <option value="">All environments</option>
                {environments.map((e) => (
                  <option key={e} value={e}>
                    {e.charAt(0).toUpperCase() + e.slice(1)}
                  </option>
                ))}
              </select>
            </label>
            <div className="hidden xl:flex">
              <TimeRangePicker />
            </div>
            <div className="hidden md:flex xl:hidden">
              <TimeRangePicker compact />
            </div>
            <LiveSwitch />
            <Notifications />
            <UserMenu />
          </div>
        </header>
        <div className="flex gap-2 border-b border-line bg-surface px-3 py-2 md:hidden">
          <select aria-label="Environment" value={env} onChange={(e) => setEnv(e.target.value)} className="min-w-0 flex-1 rounded-lg border border-line-strong bg-surface px-2 py-1.5 text-sm text-ink">
            <option value="">All environments</option>
            {environments.map((e) => (
              <option key={e} value={e}>
                {e.charAt(0).toUpperCase() + e.slice(1)}
              </option>
            ))}
          </select>
          <TimeRangePicker compact />
        </div>
        <main id="main" className="min-w-0 flex-1 overflow-y-auto px-3 py-5 lg:px-7">
          <Outlet />
        </main>
      </div>
      <CommandPalette open={palette} onClose={() => setPalette(false)} />
    </div>
  )
}

/** Heading of a page section inside a layout that already has the page title. */
export function SectionHeader({ title, subtitle, actions }: { title: ReactNode; subtitle?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mb-4 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
      <div className="min-w-0 flex-1">
        <h2 className="text-lg font-semibold tracking-tight text-ink">{title}</h2>
        {subtitle && <p className="mt-0.5 max-w-3xl text-sm text-ink-2">{subtitle}</p>}
      </div>
      {actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}

export function PageHeader({ title, subtitle, actions }: { title: ReactNode; subtitle?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mb-5 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
      <div className="min-w-0 flex-1">
        <h1 className="text-[26px] font-bold tracking-tight text-ink">{title}</h1>
        {subtitle && <p className="mt-1 max-w-3xl text-sm text-ink-2">{subtitle}</p>}
      </div>
      {actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}
