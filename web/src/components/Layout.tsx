import { NavLink, Outlet, useMatch } from 'react-router'
import { Activity, AlertTriangle, BookOpen, Database, LogOut, Moon, Settings, Sun } from 'lucide-react'
import { useAuth } from '../app/auth'
import { useServerEvents } from '../lib/events'
import { timeZoneLabel } from '../lib/format'
import { useTheme, useTimeZone } from '../lib/theme'
import { useTarget } from '../api/hooks'
import { cx, StateBadge } from './ui'

const navLink = ({ isActive }: { isActive: boolean }) =>
  cx('flex items-center gap-2 rounded-md px-2.5 py-1.5 text-sm', isActive ? 'bg-panel2 font-medium text-fg' : 'text-muted hover:bg-panel2 hover:text-fg')

function TargetNav({ id }: { id: number }) {
  const t = useTarget(id)
  const base = `/targets/${id}`
  return (
    <div className="mt-4">
      <p className="px-2.5 text-xs font-semibold uppercase tracking-wide text-muted">Target</p>
      <div className="mt-1 flex items-center justify-between gap-2 px-2.5 py-1">
        <span className="truncate text-sm font-medium">{t.data?.name ?? `#${id}`}</span>
        {t.data && <StateBadge state={t.data.status.state} />}
      </div>
      <nav aria-label="Target views" className="mt-1 space-y-0.5">
        <NavLink end to={base} className={navLink}>Overview</NavLink>
        <NavLink to={`${base}/queries`} className={navLink}>Query workload</NavLink>
        <NavLink to={`${base}/blocking`} className={navLink}>Blocking explorer</NavLink>
        <NavLink to={`${base}/sessions`} className={navLink}>Sessions & transactions</NavLink>
        <NavLink to={`${base}/tables`} className={navLink}>Tables & vacuum</NavLink>
        <NavLink to={`${base}/replication`} className={navLink}>Replication & WAL</NavLink>
      </nav>
    </div>
  )
}

export function Layout() {
  const { session, logout } = useAuth()
  const [theme, setTheme] = useTheme()
  const [tz, setTz] = useTimeZone()
  const stream = useServerEvents(Boolean(session))
  const m = useMatch('/targets/:id/*')
  const targetId = m?.params.id ? Number(m.params.id) : null
  return (
    <div className="flex min-h-full flex-col md:flex-row">
      <a href="#main" className="sr-only focus:not-sr-only focus:absolute focus:z-50 focus:m-2 focus:rounded focus:bg-panel focus:p-2">
        Skip to content
      </a>
      <aside className="border-b border-border bg-panel md:sticky md:top-0 md:h-screen md:w-60 md:shrink-0 md:overflow-y-auto md:border-r md:border-b-0">
        <div className="flex items-center justify-between px-4 py-3">
          <NavLink to="/" className="flex items-center gap-2 font-semibold">
            <Database aria-hidden className="h-5 w-5 text-accent" /> LockLens
          </NavLink>
        </div>
        <div className="px-2 pb-4">
          <nav aria-label="Main" className="flex flex-wrap gap-0.5 md:block md:space-y-0.5">
            <NavLink end to="/" className={navLink}>
              <Activity aria-hidden className="h-4 w-4" /> Fleet
            </NavLink>
            <NavLink to="/incidents" className={navLink}>
              <AlertTriangle aria-hidden className="h-4 w-4" /> Incidents
            </NavLink>
            <NavLink to="/settings" className={navLink}>
              <Settings aria-hidden className="h-4 w-4" /> Targets & settings
            </NavLink>
            {session?.server.demo_mode && (
              <NavLink to="/lab" className={navLink}>
                <BookOpen aria-hidden className="h-4 w-4" /> Lab guide
              </NavLink>
            )}
          </nav>
          {targetId !== null && Number.isFinite(targetId) && <TargetNav id={targetId} />}
        </div>
      </aside>
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex flex-wrap items-center justify-end gap-3 border-b border-border bg-panel px-4 py-2 text-sm">
          <span role="status" aria-live="polite" className={cx('flex items-center gap-1.5', stream === 'connected' ? 'text-muted' : 'text-warn')}>
            <span aria-hidden className={cx('h-2 w-2 rounded-full', stream === 'connected' ? 'bg-ok' : 'bg-warn')} />
            {stream === 'connected' ? 'Live updates' : stream === 'connecting' ? 'Connecting to live updates…' : 'Live updates disconnected (polling)'}
          </span>
          <label className="flex items-center gap-1.5">
            <span className="text-muted">Times in</span>
            <select className="rounded-md border border-border bg-panel px-1.5 py-0.5" value={tz} onChange={(e) => setTz(e.target.value === 'utc' ? 'utc' : 'local')}>
              <option value="local">{timeZoneLabel('local')}</option>
              <option value="utc">UTC</option>
            </select>
          </label>
          <button
            type="button"
            className="rounded-md border border-border p-1.5 hover:bg-panel2"
            onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}
            aria-label={`Switch to ${theme === 'dark' ? 'light' : 'dark'} theme`}
          >
            {theme === 'dark' ? <Sun aria-hidden className="h-4 w-4" /> : <Moon aria-hidden className="h-4 w-4" />}
          </button>
          <span className="text-muted">{session?.username}</span>
          <button type="button" onClick={() => void logout()} className="flex items-center gap-1 rounded-md border border-border px-2 py-1 hover:bg-panel2">
            <LogOut aria-hidden className="h-4 w-4" /> Log out
          </button>
        </header>
        <main id="main" className="min-w-0 flex-1 p-4 md:p-6">
          <Outlet key={tz} />
        </main>
      </div>
    </div>
  )
}
