import { lazy, Suspense, type ReactNode } from 'react'
import { Navigate, Route, Routes, useLocation } from 'react-router'
import type { Permission } from './api/types'
import { useAuth, useCan } from './auth'
import { Shell } from './components/Shell'
import { Spinner } from './components/ui'
import { LoginPage } from './pages/LoginPage'

const OverviewPage = lazy(() => import('./pages/OverviewPage'))
const InstancesPage = lazy(() => import('./pages/InstancesPage'))
const DatabasePage = lazy(() => import('./pages/DatabasePage'))
const DatabaseOverview = lazy(() => import('./pages/DatabasePage').then((m) => ({ default: m.DatabaseOverview })))
const ActivityPage = lazy(() => import('./pages/ActivityPage'))
const QueriesPage = lazy(() => import('./pages/QueriesPage'))
const ReplicationPage = lazy(() => import('./pages/ReplicationPage'))
const LoadPage = lazy(() => import('./pages/LoadPage'))
const AlertsPage = lazy(() => import('./pages/AlertsPage'))
const TopologyPage = lazy(() => import('./pages/TopologyPage'))
const FleetQueriesPage = lazy(() => import('./pages/FleetQueriesPage'))
const QueryDetailPage = lazy(() => import('./pages/QueryDetailPage'))
const SessionsPage = lazy(() => import('./pages/SessionsPage'))
const BlockingPage = lazy(() => import('./pages/BlockingPage'))
const ExplorerPage = lazy(() => import('./pages/ExplorerPage'))
const SqlPage = lazy(() => import('./pages/SqlPage'))
const IncidentsPage = lazy(() => import('./pages/IncidentsPage'))
const IncidentPage = lazy(() => import('./pages/IncidentPage'))
const ConnectionsPage = lazy(() => import('./pages/ConnectionsPage'))
const SettingsPage = lazy(() => import('./pages/SettingsPage'))
const TargetFormPage = lazy(() => import('./pages/TargetFormPage'))
const QuickAddPage = lazy(() => import('./pages/QuickAddPage'))
const AccountPage = lazy(() => import('./pages/AccountPage'))

/** Shows the page only to roles with the permission (the API enforces it too). */
function RequirePermission({ perm, children }: { perm: Permission; children: ReactNode }) {
  const can = useCan()
  if (can(perm)) return <>{children}</>
  return (
    <div className="py-16 text-center">
      <h1 className="text-lg font-semibold">Not available for your role</h1>
      <p className="mt-1 text-sm text-ink-2">Ask an administrator if you need access.</p>
    </div>
  )
}

function NotFound() {
  return (
    <div className="py-16 text-center">
      <h1 className="text-lg font-semibold">Page not found</h1>
      <a className="mt-2 inline-block text-sm text-accent underline" href="/">
        Go to the overview
      </a>
    </div>
  )
}

export function App() {
  const { session, checking } = useAuth()
  const location = useLocation()
  if (checking) return <Spinner label="Checking session" />
  if (!session) {
    return location.pathname === '/login' ? <LoginPage /> : <Navigate to={`/login?next=${encodeURIComponent(location.pathname + location.search)}`} replace />
  }
  return (
    <Suspense fallback={<Spinner />}>
      <Routes>
        <Route path="/login" element={<Navigate to="/overview" replace />} />
        <Route element={<Shell />}>
          <Route index element={<Navigate to="/overview" replace />} />
          <Route path="overview" element={<OverviewPage />} />
          <Route path="databases" element={<InstancesPage />} />
          <Route path="databases/:id" element={<DatabasePage />}>
            <Route index element={<DatabaseOverview />} />
            <Route path="load" element={<LoadPage />} />
            <Route path="queries" element={<QueriesPage />} />
            <Route path="activity" element={<ActivityPage />} />
            <Route path="replication" element={<ReplicationPage />} />
            <Route path="alerts" element={<AlertsPage />} />
          </Route>
          <Route path="topology" element={<TopologyPage />} />
          <Route path="queries" element={<FleetQueriesPage />} />
          <Route path="queries/:targetId/:qid" element={<QueryDetailPage />} />
          <Route path="sessions" element={<SessionsPage />} />
          <Route path="blocking" element={<BlockingPage />} />
          <Route path="explorer" element={<ExplorerPage />} />
          <Route path="explorer/:targetId" element={<ExplorerPage />} />
          <Route path="explorer/:targetId/:schema" element={<ExplorerPage />} />
          <Route path="explorer/:targetId/:schema/:object" element={<ExplorerPage />} />
          <Route path="incidents" element={<IncidentsPage />} />
          <Route path="connections" element={<ConnectionsPage />} />
          <Route path="incidents/:incidentId" element={<IncidentPage />} />
          <Route path="sql" element={<RequirePermission perm="workspace.sql"><SqlPage /></RequirePermission>} />
          <Route path="sql/:targetId" element={<RequirePermission perm="workspace.sql"><SqlPage /></RequirePermission>} />
          <Route path="account" element={<AccountPage />} />
          <Route path="settings" element={<RequirePermission perm="targets.manage"><SettingsPage /></RequirePermission>} />
          <Route path="settings/targets/new" element={<RequirePermission perm="targets.manage"><QuickAddPage /></RequirePermission>} />
          <Route path="settings/targets/new/manual" element={<RequirePermission perm="targets.manage"><TargetFormPage /></RequirePermission>} />
          <Route path="settings/targets/:targetId" element={<RequirePermission perm="targets.manage"><TargetFormPage /></RequirePermission>} />
          <Route path="*" element={<NotFound />} />
        </Route>
      </Routes>
    </Suspense>
  )
}
