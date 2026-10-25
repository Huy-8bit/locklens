import { useState, type FormEvent } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import { Database } from 'lucide-react'
import { useAuth } from '../auth'
import { Button, Field, inputCls } from '../components/ui'

export function LoginPage() {
  const { login } = useAuth()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await login(username, password)
      const next = params.get('next')
      navigate(next && next.startsWith('/') && !next.startsWith('//') ? next : '/', { replace: true })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Sign-in failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="flex min-h-full items-center justify-center bg-bg p-4">
      <form onSubmit={(e) => void submit(e)} aria-labelledby="login-title" className="w-full max-w-sm space-y-4 rounded-2xl border border-line bg-surface p-6 shadow-sm">
        <div className="flex items-center gap-2">
          <Database aria-hidden className="h-6 w-6 text-accent" />
          <h1 id="login-title" className="text-lg font-semibold">
            Sign in to LockLens
          </h1>
        </div>
        <p className="text-sm text-muted">PostgreSQL monitoring. LockLens only reads from your databases.</p>
        <Field label="Username">
          <input className={inputCls} autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} required />
        </Field>
        <Field label="Password">
          <input className={inputCls} type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
        </Field>
        {error && (
          <p role="alert" className="text-sm text-crit">
            {error}
          </p>
        )}
        <Button type="submit" variant="primary" busy={busy} className="w-full">
          Sign in
        </Button>
      </form>
    </main>
  )
}
