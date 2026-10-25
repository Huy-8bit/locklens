import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Link, NavLink, Outlet, useLocation, useParams } from 'react-router'
import {
  Bell,
  Cable,
  CalendarDays,
  ChevronDown,
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
import { useAuth } from '../auth'
import { useEventStream } from '../lib/events'
import { RANGES, useScope, type Range } from '../lib/scope'
import { useTheme } from '../lib/theme'
import { CommandPalette } from './CommandPalette'
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
        cx('flex items-center gap-3 rounded-lg px-3 py-2 text-[13.5px] font-medium transition-colors', isActive ? 'bg-nav-active text-white shadow-sm' : 'text-nav-muted hover:bg-nav-hover hover:text-nav-ink')
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
  const rows = statusRows(q.data)
  const allOk = rows.length > 0 && rows.every((r) => r.ok)
  return (
    <section aria-label="System status" className="rounded-xl border border-white/10 bg-white/[0.04] p-3">
      <h2 className="text-[12.5px] font-semibold text-nav-ink">System Status</h2>
      <p className="mt-1 flex items-center gap-2 text-[12.5px] text-nav-ink">
        {q.data ? (
          allOk ? (
            <>
              <CircleCheck aria-hidden className="h-4 w-4 text-[#4ade80]" /> All systems operational
            </>
          ) : (
            <>
              <TriangleAlert aria-hidden className="h-4 w-4 text-[#fbbf24]" /> Attention needed
            </>
          )
        ) : (
          'Checking…'
        )}
      </p>
      <ul className="mt-2 space-y-1.5">
        {rows.map((r) => (
          <li key={r.label} className="flex items-center justify-between gap-2 text-[12.5px]">
            <span className="flex items-center gap-2 text-nav-muted">
              {r.ok ? <CircleCheck aria-hidden className="h-4 w-4 text-[#4ade80]" /> : <OctagonAlert aria-hidden className="h-4 w-4 text-[#f87171]" />}
              {r.label}
            </span>
            <span className={r.ok ? 'text-[#4ade80]' : 'text-[#fca5a5]'}>{r.detail}</span>
          </li>
        ))}
      </ul>
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
          <span className="block text-xs text-muted">Administrator</span>
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
          <Link role="menuitem" to="/settings" onClick={() => setOpen(false)} className="flex items-center gap-2 rounded-lg px-2.5 py-2 text-sm hover:bg-surface-2">
            <Settings aria-hidden className="h-4 w-4" /> Settings
          </Link>
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
    <button type="button" role="switch" aria-checked={live} onClick={() => setLive(!live)} className="flex items-center gap-2 rounded-lg px-1.5 py-1 text-sm font-medium text-ink-2 hover:bg-surface-2">
      <span aria-hidden className={cx('relative inline-block h-6 w-11 rounded-full transition-colors', live ? 'bg-[#16a34a]' : 'bg-line-strong')}>
        <span className={cx('absolute top-0.5 h-5 w-5 rounded-full bg-white shadow transition-all', live ? 'left-[22px]' : 'left-0.5')} />
      </span>
      {live ? 'Live' : 'Paused'}
    </button>
  )
}

export function Shell() {
  const { env, setEnv, range, setRange } = useScope()
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

  const sidebar = (
    <nav aria-label="Main" className="flex h-full flex-col gap-5 overflow-y-auto bg-nav px-3 py-5">
      <Link to="/" className="flex items-center gap-3 px-2">
        <Database aria-hidden className="h-9 w-9 text-[#3b82f6]" strokeWidth={2.2} />
        <span className="leading-tight">
          <span className="block text-[19px] font-bold tracking-tight text-white">LockLens</span>
          <span className="block text-[11px] text-nav-muted">PostgreSQL Monitoring &amp; Administration</span>
        </span>
      </Link>
      <div className="space-y-1">
        <NavItem to="/overview" icon={<LayoutDashboard />}>Overview</NavItem>
        <NavItem to="/databases" icon={<Database />}>Databases</NavItem>
        <NavItem to="/topology" icon={<Network />}>Topology</NavItem>
        <NavItem to="/queries" icon={<ScrollText />}>Queries</NavItem>
        <NavItem to="/sessions" icon={<Users />}>Sessions</NavItem>
        <NavItem to="/blocking" icon={<Lock />}>Blocking</NavItem>
        <NavItem to="/explorer" icon={<FolderTree />}>Explorer</NavItem>
        <NavItem to="/sql" icon={<SquareTerminal />}>SQL Editor</NavItem>
        <NavItem to="/incidents" icon={<TriangleAlert />} badge={openCount > 0 ? <span className="rounded-full bg-[#dc2626] px-2 text-[11px] font-semibold leading-5 text-white">{openCount}</span> : undefined}>
          Incidents
        </NavItem>
        <NavItem to="/connections" icon={<Cable />}>Connections</NavItem>
        <NavItem to="/settings" icon={<Settings />}>Settings</NavItem>
      </div>
      <div className="mt-auto">
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
            className="flex min-w-0 flex-1 items-center gap-2.5 rounded-xl border border-line bg-surface-2 px-3 py-2 text-left text-sm text-muted hover:border-line-strong md:max-w-md xl:max-w-lg"
          >
            <Search aria-hidden className="h-4 w-4 shrink-0" />
            <span className="truncate">Search databases, queries, sessions, or anything…</span>
            <kbd className="ml-auto hidden rounded border border-line-strong bg-surface px-1.5 font-sans text-[11px] text-ink-2 sm:inline">⌘ K</kbd>
          </button>
          <div className="ml-auto flex items-center gap-1.5 lg:gap-3">
            <label className="hidden items-center gap-2 rounded-xl border border-line bg-surface px-3 py-2 text-sm font-medium md:flex">
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
            <label className="hidden items-center gap-2 rounded-xl border border-line bg-surface px-3 py-2 text-sm font-medium md:flex">
              <CalendarDays aria-hidden className="h-4 w-4 text-ink-2" />
              <span className="sr-only">Time range</span>
              <select value={range} onChange={(e) => setRange(e.target.value as Range)} className="bg-transparent pr-1 text-sm font-medium text-ink">
                {RANGES.map((r) => (
                  <option key={r.value} value={r.value}>
                    {r.label}
                  </option>
                ))}
              </select>
            </label>
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
          <select aria-label="Time range" value={range} onChange={(e) => setRange(e.target.value as Range)} className="min-w-0 flex-1 rounded-lg border border-line-strong bg-surface px-2 py-1.5 text-sm text-ink">
            {RANGES.map((r) => (
              <option key={r.value} value={r.value}>
                {r.label}
              </option>
            ))}
          </select>
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
    <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
      <div className="min-w-0">
        <h2 className="text-lg font-semibold tracking-tight text-ink">{title}</h2>
        {subtitle && <p className="mt-0.5 text-sm text-ink-2">{subtitle}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}

export function PageHeader({ title, subtitle, actions }: { title: ReactNode; subtitle?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mb-5 flex flex-wrap items-end justify-between gap-3">
      <div className="min-w-0">
        <h1 className="text-[26px] font-bold tracking-tight text-ink">{title}</h1>
        {subtitle && <p className="mt-1 text-sm text-ink-2">{subtitle}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}
