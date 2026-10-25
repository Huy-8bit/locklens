import { lazy, Suspense } from 'react'
import { Navigate, Route, Routes, useLocation } from 'react-router'
import { useAuth } from './auth'
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
          <Route path="sql" element={<SqlPage />} />
          <Route path="sql/:targetId" element={<SqlPage />} />
          <Route path="incidents" element={<IncidentsPage />} />
          <Route path="connections" element={<ConnectionsPage />} />
          <Route path="incidents/:incidentId" element={<IncidentPage />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="settings/targets/new" element={<TargetFormPage />} />
          <Route path="settings/targets/:targetId" element={<TargetFormPage />} />
          <Route path="*" element={<NotFound />} />
        </Route>
      </Routes>
    </Suspense>
  )
}
