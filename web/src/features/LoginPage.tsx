import { useState, type FormEvent } from 'react'
import { Navigate, useLocation } from 'react-router'
import { Database } from 'lucide-react'
import { ApiError } from '../api/client'
import { useAuth } from '../app/auth'
import { Button } from '../components/ui'

export function LoginPage() {
  const { session, login } = useAuth()
  const location = useLocation()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const from = (location.state as { from?: string } | null)?.from ?? '/'
  if (session) return <Navigate to={from} replace />

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await login(username, password)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Login failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="flex min-h-full items-center justify-center p-4">
      <form onSubmit={(e) => void submit(e)} className="w-full max-w-sm space-y-4 rounded-lg border border-border bg-panel p-6" aria-labelledby="login-title">
        <div className="flex items-center gap-2">
          <Database aria-hidden className="h-6 w-6 text-accent" />
          <h1 id="login-title" className="text-lg font-semibold">
            Sign in to LockLens
          </h1>
        </div>
        <p className="text-sm text-muted">PostgreSQL monitoring and incident investigation. LockLens only observes; it never changes your databases.</p>
        <label className="block text-sm">
          <span className="mb-1 block font-medium">Username</span>
          <input
            className="w-full rounded-md border border-border bg-bg px-3 py-2"
            autoComplete="username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            required
          />
        </label>
        <label className="block text-sm">
          <span className="mb-1 block font-medium">Password</span>
          <input
            type="password"
            className="w-full rounded-md border border-border bg-bg px-3 py-2"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </label>
        {error && (
          <p role="alert" className="text-sm text-crit">
            {error}
          </p>
        )}
        <Button type="submit" variant="primary" busy={busy} className="w-full justify-center">
          Sign in
        </Button>
      </form>
    </main>
  )
}
