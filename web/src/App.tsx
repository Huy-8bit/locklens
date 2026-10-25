import { lazy, Suspense } from 'react'
import { Navigate, Route, Routes, useLocation } from 'react-router'
import { useFleet } from './api/hooks'
import { useAuth } from './auth'
import { lastInstance, Shell } from './components/Shell'
import { Spinner } from './components/ui'
import { LoginPage } from './pages/LoginPage'

const InstancesPage = lazy(() => import('./pages/InstancesPage'))
const OverviewPage = lazy(() => import('./pages/OverviewPage'))
const ActivityPage = lazy(() => import('./pages/ActivityPage'))
const QueriesPage = lazy(() => import('./pages/QueriesPage'))
const QueryDetailPage = lazy(() => import('./pages/QueryDetailPage'))
const ExplorerPage = lazy(() => import('./pages/ExplorerPage'))
const ReplicationPage = lazy(() => import('./pages/ReplicationPage'))
const AlertsPage = lazy(() => import('./pages/AlertsPage'))
const IncidentPage = lazy(() => import('./pages/IncidentPage'))
const SettingsPage = lazy(() => import('./pages/SettingsPage'))
const TargetFormPage = lazy(() => import('./pages/TargetFormPage'))

/** Opens the last viewed instance, else the first one, else the instance list. */
function Home() {
  const fleet = useFleet()
  if (!fleet.data) return <Spinner label="Loading instances" />
  const ids = fleet.data.targets.map((t) => t.target.id)
  const last = lastInstance()
  const id = last && ids.includes(last) ? last : ids[0]
  return <Navigate to={id ? `/i/${id}/overview` : '/instances'} replace />
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
        <Route path="/login" element={<Navigate to="/" replace />} />
        <Route element={<Shell />}>
          <Route index element={<Home />} />
          <Route path="instances" element={<InstancesPage />} />
          <Route path="i/:id">
            <Route index element={<Navigate to="overview" replace />} />
            <Route path="overview" element={<OverviewPage />} />
            <Route path="activity" element={<ActivityPage />} />
            <Route path="queries" element={<QueriesPage />} />
            <Route path="queries/:qid" element={<QueryDetailPage />} />
            <Route path="explorer" element={<ExplorerPage />} />
            <Route path="explorer/:schema" element={<ExplorerPage />} />
            <Route path="explorer/:schema/:table" element={<ExplorerPage />} />
            <Route path="replication" element={<ReplicationPage />} />
            <Route path="alerts" element={<AlertsPage />} />
          </Route>
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
