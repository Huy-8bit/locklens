import type { ReactNode } from 'react'
import { Navigate, Route, Routes, useLocation } from 'react-router'
import { useAuth } from './auth'
import { Layout } from '../components/Layout'
import { Spinner } from '../components/ui'
import { BlockingPage } from '../features/BlockingPage'
import { FleetPage } from '../features/FleetPage'
import { IncidentDetailPage } from '../features/IncidentDetailPage'
import { IncidentsPage } from '../features/IncidentsPage'
import { LabGuidePage } from '../features/LabGuidePage'
import { LoginPage } from '../features/LoginPage'
import { NotFoundPage } from '../features/NotFoundPage'
import { QueriesPage } from '../features/QueriesPage'
import { QueryDetailPage } from '../features/QueryDetailPage'
import { ReplicationPage } from '../features/ReplicationPage'
import { SessionsPage } from '../features/SessionsPage'
import { SettingsPage, TargetFormPage } from '../features/SettingsPage'
import { TablesPage } from '../features/TablesPage'
import { TargetOverviewPage } from '../features/TargetOverviewPage'

function RequireAuth({ children }: { children: ReactNode }) {
  const { session, loading } = useAuth()
  const location = useLocation()
  if (loading) {
    return (
      <div className="p-6">
        <Spinner label="Checking session" />
      </div>
    )
  }
  if (!session) return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />
  return <>{children}</>
}

export function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route
        element={
          <RequireAuth>
            <Layout />
          </RequireAuth>
        }
      >
        <Route index element={<FleetPage />} />
        <Route path="targets/:id" element={<TargetOverviewPage />} />
        <Route path="targets/:id/queries" element={<QueriesPage />} />
        <Route path="targets/:id/queries/:qid" element={<QueryDetailPage />} />
        <Route path="targets/:id/blocking" element={<BlockingPage />} />
        <Route path="targets/:id/sessions" element={<SessionsPage />} />
        <Route path="targets/:id/tables" element={<TablesPage />} />
        <Route path="targets/:id/replication" element={<ReplicationPage />} />
        <Route path="incidents" element={<IncidentsPage />} />
        <Route path="incidents/:id" element={<IncidentDetailPage />} />
        <Route path="settings" element={<SettingsPage />} />
        <Route path="settings/targets/new" element={<TargetFormPage />} />
        <Route path="settings/targets/:id" element={<TargetFormPage />} />
        <Route path="lab" element={<LabGuidePage />} />
        <Route path="*" element={<NotFoundPage />} />
      </Route>
    </Routes>
  )
}
