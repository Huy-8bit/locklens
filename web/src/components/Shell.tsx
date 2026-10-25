import { useEffect, useState, type ReactNode } from 'react'
import { NavLink, Outlet, useLocation, useNavigate, useParams } from 'react-router'
import { Activity, Bell, Database, Gauge, LayoutGrid, LogOut, Menu, Moon, Network, ScrollText, Settings, Sun, Table2, X } from 'lucide-react'
import { useFleet } from '../api/hooks'
import { useAuth } from '../auth'
import { useEventStream } from '../lib/events'
import { ago } from '../lib/format'
import { useTheme } from '../lib/theme'
import { Badge, cx, StateDot } from './ui'

const LAST = 'locklens-last-instance'

export function rememberInstance(id: number) {
  try {
    localStorage.setItem(LAST, String(id))
  } catch {
    // ignore: storage is only a convenience
  }
}

export function lastInstance(): number | null {
  try {
    const v = Number(localStorage.getItem(LAST))
    return Number.isInteger(v) && v > 0 ? v : null
  } catch {
    return null
  }
}

/** The instance id of the current route (/i/:id/...), or 0. */
export function useInstanceId(): number {
  const id = Number(useParams().id)
  return Number.isInteger(id) && id > 0 ? id : 0
}

function NavItem({ to, icon, children, badge, end }: { to: string; icon: ReactNode; children: ReactNode; badge?: ReactNode; end?: boolean }) {
  return (
    <NavLink
      to={to}
      end={end}
      className={({ isActive }) => cx('flex items-center gap-2.5 rounded-lg px-2.5 py-1.5 text-[13px]', isActive ? 'bg-nav-active font-medium text-nav-ink' : 'text-nav-muted hover:bg-nav-active hover:text-nav-ink')}
    >
      <span aria-hidden className="shrink-0 [&>svg]:h-4 [&>svg]:w-4">{icon}</span>
      <span className="min-w-0 flex-1 truncate">{children}</span>
      {badge}
    </NavLink>
  )
}

export function Shell() {
  const { session, logout } = useAuth()
  const [theme, toggleTheme] = useTheme()
  const stream = useEventStream(true)
  const fleet = useFleet()
  const id = useInstanceId()
  const navigate = useNavigate()
  const location = useLocation()
  // The mobile menu belongs to the page it was opened on, so navigating closes it.
  const [menuPath, setMenuPath] = useState<string | null>(null)
  const menuOpen = menuPath === location.pathname
  const setMenuOpen = (open: boolean) => setMenuPath(open ? location.pathname : null)
  const targets = fleet.data?.targets ?? []
  const current = targets.find((t) => t.target.id === id)

  useEffect(() => {
    if (id) rememberInstance(id)
  }, [id])

  // Switching instance keeps the current view (overview, queries, ...).
  const switchTo = (next: number) => {
    const view = location.pathname.match(/^\/i\/\d+\/([a-z]+)/)?.[1] ?? 'overview'
    navigate(`/i/${next}/${view}${location.search}`)
  }

  const openIncidents = current?.counts.open_incidents ?? 0
  const sidebar = (
    <nav aria-label="Main" className="flex h-full flex-col gap-4 overflow-y-auto bg-nav px-3 py-4 text-nav-ink">
      <div className="flex items-center gap-2 px-1.5">
        <Database aria-hidden className="h-5 w-5 text-[#6da7ec]" />
        <span className="text-[15px] font-semibold tracking-tight">LockLens</span>
      </div>

      <div className="px-0.5">
        <label htmlFor="instance" className="mb-1 block px-1 text-[11px] font-medium uppercase tracking-wider text-nav-muted">
          Database instance
        </label>
        <select
          id="instance"
          value={id || ''}
          onChange={(e) => switchTo(Number(e.target.value))}
          className="w-full rounded-lg border border-white/10 bg-white/5 px-2 py-1.5 text-[13px] text-nav-ink"
        >
          {!id && <option value="">Choose an instance…</option>}
          {targets.map((t) => (
            <option key={t.target.id} value={t.target.id} className="text-black">
              {t.target.name} — {t.target.status.state}
            </option>
          ))}
        </select>
      </div>

      {id > 0 && (
        <div className="space-y-0.5">
          <NavItem to={`/i/${id}/overview`} icon={<Gauge />}>
            Overview
          </NavItem>
          <NavItem to={`/i/${id}/activity`} icon={<Activity />}>
            Live activity
          </NavItem>
          <NavItem to={`/i/${id}/queries`} icon={<ScrollText />}>
            Queries
          </NavItem>
          <NavItem to={`/i/${id}/explorer`} icon={<Table2 />}>
            Data explorer
          </NavItem>
          <NavItem to={`/i/${id}/replication`} icon={<Network />}>
            Replication
          </NavItem>
          <NavItem to={`/i/${id}/alerts`} icon={<Bell />} badge={openIncidents > 0 ? <span className="rounded-full bg-[#d03b3b] px-1.5 text-[11px] font-semibold text-white">{openIncidents}</span> : undefined}>
            Alerts
          </NavItem>
        </div>
      )}

      <div className="mt-auto space-y-0.5 border-t border-white/10 pt-3">
        <NavItem to="/instances" icon={<LayoutGrid />}>
          All instances
        </NavItem>
        <NavItem to="/settings" icon={<Settings />}>
          Settings
        </NavItem>
      </div>
    </nav>
  )

  return (
    <div className="flex h-full">
      <a href="#main" className="sr-only focus:not-sr-only focus:absolute focus:z-50 focus:m-2 focus:rounded focus:bg-surface focus:px-3 focus:py-2">
        Skip to content
      </a>
      <aside className="hidden w-60 shrink-0 lg:block">{sidebar}</aside>
      {menuOpen && (
        <div className="fixed inset-0 z-40 flex lg:hidden" onClick={() => setMenuOpen(false)}>
          <div className="w-64" onClick={(e) => e.stopPropagation()}>
            {sidebar}
          </div>
          <div className="flex-1 bg-black/40" />
        </div>
      )}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-12 shrink-0 items-center gap-3 border-b border-line bg-surface px-3 lg:px-5">
          <button type="button" className="rounded-md p-1.5 text-ink-2 hover:bg-surface-2 lg:hidden" aria-label={menuOpen ? 'Close menu' : 'Open menu'} onClick={() => setMenuOpen(!menuOpen)}>
            {menuOpen ? <X aria-hidden className="h-5 w-5" /> : <Menu aria-hidden className="h-5 w-5" />}
          </button>
          {current ? (
            <div className="flex min-w-0 items-center gap-2 text-sm">
              <StateDot state={current.target.status.state} />
              <span className="truncate font-semibold">{current.target.name}</span>
              <span className="hidden truncate text-muted sm:inline">
                {current.target.host}:{current.target.port}/{current.target.database}
                {current.target.status.server_version ? ` · PostgreSQL ${current.target.status.server_version}` : ''}
                {current.target.status.is_standby ? ' · standby' : current.target.status.is_standby === false ? ' · primary' : ''}
              </span>
            </div>
          ) : (
            <span className="text-sm font-semibold">LockLens</span>
          )}
          <div className="ml-auto flex items-center gap-2 text-xs">
            {current && <span className="hidden text-muted md:inline">updated {ago(current.target.status.last_success_at)}</span>}
            <Badge tone={stream === 'live' ? 'good' : stream === 'offline' ? 'warn' : 'muted'} title="Server event stream">
              <span aria-hidden className="h-1.5 w-1.5 rounded-full bg-current" />
              {stream === 'live' ? 'Live' : stream === 'offline' ? 'Reconnecting' : 'Connecting'}
            </Badge>
            <button type="button" onClick={toggleTheme} aria-label={`Switch to ${theme === 'dark' ? 'light' : 'dark'} theme`} className="rounded-md p-1.5 text-ink-2 hover:bg-surface-2">
              {theme === 'dark' ? <Sun aria-hidden className="h-4 w-4" /> : <Moon aria-hidden className="h-4 w-4" />}
            </button>
            <span className="hidden text-ink-2 sm:inline">{session?.username}</span>
            <button type="button" onClick={() => void logout()} className="inline-flex items-center gap-1 rounded-md px-2 py-1.5 text-ink-2 hover:bg-surface-2">
              <LogOut aria-hidden className="h-4 w-4" />
              <span className="hidden sm:inline">Log out</span>
            </button>
          </div>
        </header>
        <main id="main" className="min-w-0 flex-1 overflow-y-auto px-3 py-4 lg:px-6 lg:py-5">
          <Outlet />
        </main>
      </div>
    </div>
  )
}

export function PageHeader({ title, subtitle, actions }: { title: ReactNode; subtitle?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
      <div className="min-w-0">
        <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
        {subtitle && <p className="mt-0.5 text-sm text-muted">{subtitle}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}
