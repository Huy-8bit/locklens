import { useRef, useState, type FormEvent, type KeyboardEvent, type ReactNode } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import { Activity, AlertTriangle, Database, Eye, EyeOff, Loader2, Lock, ShieldCheck, User, Workflow } from 'lucide-react'
import { ApiError } from '../api/client'
import { useAuth } from '../auth'
import { cx } from '../components/ui'

// The last user name is remembered per browser as a convenience only.
const LAST_USER = 'locklens-last-user'

function lastUser(): string {
  try {
    return localStorage.getItem(LAST_USER) ?? ''
  } catch {
    return ''
  }
}

function rememberUser(name: string) {
  try {
    localStorage.setItem(LAST_USER, name)
  } catch {
    // storage unavailable: nothing to remember
  }
}

/** A sentence for the sign-in error, by API error code. */
function signInError(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === 'invalid_credentials') return 'The username or password is not correct.'
    if (err.code === 'rate_limited') return 'Too many failed attempts. Wait a minute, then try again.'
    if (err.code === 'network') return 'Cannot reach the LockLens server. Check that it is running.'
  }
  return err instanceof Error ? err.message : 'Sign-in failed.'
}

function Feature({ icon, title, children }: { icon: ReactNode; title: string; children: ReactNode }) {
  return (
    <li className="flex gap-4">
      <span aria-hidden className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-white/[0.07] text-[#93c5fd] ring-1 ring-white/10 [&>svg]:h-5 [&>svg]:w-5">
        {icon}
      </span>
      <span>
        <span className="block text-[15px] font-semibold text-white">{title}</span>
        <span className="mt-0.5 block text-sm leading-relaxed text-nav-muted">{children}</span>
      </span>
    </li>
  )
}

function Brand({ inverted = false }: { inverted?: boolean }) {
  return (
    <span className="flex items-center gap-3">
      <span aria-hidden className={cx('flex h-10 w-10 items-center justify-center rounded-xl', inverted ? 'bg-[#2563eb] text-white shadow-lg shadow-blue-900/40' : 'bg-accent-fill text-accent-ink')}>
        <Database className="h-5 w-5" strokeWidth={2.2} />
      </span>
      <span className="leading-tight">
        <span className={cx('block text-lg font-bold tracking-tight', inverted ? 'text-white' : 'text-ink')}>LockLens</span>
        <span className={cx('block text-xs', inverted ? 'text-nav-muted' : 'text-muted')}>PostgreSQL Monitoring &amp; Administration</span>
      </span>
    </span>
  )
}

const fieldCls =
  'h-11 w-full rounded-xl border border-line-strong bg-surface pl-10 pr-3 text-[15px] text-ink placeholder:text-muted transition-shadow focus:border-accent focus:outline-none focus:ring-4 focus:ring-accent/15'

export function LoginPage() {
  const { login } = useAuth()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const [username, setUsername] = useState(lastUser)
  const [password, setPassword] = useState('')
  const [show, setShow] = useState(false)
  const [capsLock, setCapsLock] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const passwordRef = useRef<HTMLInputElement>(null)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await login(username.trim(), password)
      rememberUser(username.trim())
      const next = params.get('next')
      navigate(next && next.startsWith('/') && !next.startsWith('//') ? next : '/', { replace: true })
    } catch (err) {
      setError(signInError(err))
      setPassword('')
      passwordRef.current?.focus()
    } finally {
      setBusy(false)
    }
  }
  const caps = (e: KeyboardEvent<HTMLInputElement>) => setCapsLock(e.getModifierState('CapsLock'))

  return (
    <main className="grid min-h-full bg-bg lg:grid-cols-[minmax(0,1.05fr)_minmax(0,1fr)]">
      <aside aria-label="About LockLens" className="relative hidden overflow-hidden border-r border-white/10 bg-nav lg:flex lg:flex-col lg:justify-between lg:p-12 xl:p-16">
        {/* Decorative background: a soft glow and a fine dot grid. */}
        <div aria-hidden className="pointer-events-none absolute inset-0" style={{ backgroundImage: 'radial-gradient(circle at 20% 15%, rgba(37,99,235,0.35), transparent 45%), radial-gradient(circle at 85% 90%, rgba(14,165,233,0.18), transparent 40%)' }} />
        <div aria-hidden className="pointer-events-none absolute inset-0 opacity-[0.16]" style={{ backgroundImage: 'radial-gradient(rgba(255,255,255,0.7) 1px, transparent 1px)', backgroundSize: '22px 22px', maskImage: 'linear-gradient(to bottom, black, transparent 85%)' }} />
        <div className="relative">
          <Brand inverted />
        </div>
        <div className="relative max-w-lg">
          <h2 className="text-[34px] font-bold leading-tight tracking-tight text-white">
            See what your PostgreSQL is doing, <span className="text-[#93c5fd]">and why.</span>
          </h2>
          <p className="mt-4 text-base leading-relaxed text-nav-muted">One place for your fleet: live sessions, queries, blocking, replication, and incidents with the evidence to explain them.</p>
          <ul className="mt-10 space-y-6">
            <Feature icon={<Activity />} title="Live workload">
              Sessions, connections, and every statement grouped by type, across instances and environments.
            </Feature>
            <Feature icon={<Workflow />} title="Root causes, not just charts">
              Blocking chains with their root blocker, replication lag, and incidents recorded with evidence.
            </Feature>
            <Feature icon={<ShieldCheck />} title="Observe-only by design">
              Read-only roles and transactions. LockLens never kills sessions or changes your servers.
            </Feature>
          </ul>
        </div>
        <p className="relative text-xs text-nav-muted">Open source · MIT licensed</p>
      </aside>

      <section className="flex items-center justify-center px-5 py-12 sm:px-8">
        <div className="w-full max-w-[400px]">
          <div className="mb-10 lg:hidden">
            <Brand />
          </div>
          <form onSubmit={(e) => void submit(e)} aria-labelledby="login-title" className="space-y-5">
            <div>
              <h1 id="login-title" className="text-[28px] font-bold tracking-tight text-ink">
                Sign in to LockLens
              </h1>
              <p className="mt-1.5 text-[15px] text-ink-2">Use your LockLens account. Ask an administrator if you need one.</p>
            </div>

            {error && (
              <div role="alert" className="flex items-start gap-2.5 rounded-xl border border-crit/30 bg-crit-soft px-3.5 py-3 text-sm text-crit">
                <AlertTriangle aria-hidden className="mt-0.5 h-4 w-4 shrink-0" />
                <span>{error}</span>
              </div>
            )}

            <div>
              <label htmlFor="login-username" className="mb-1.5 block text-sm font-medium text-ink">
                Username
              </label>
              <div className="relative">
                <User aria-hidden className="pointer-events-none absolute top-1/2 left-3.5 h-[18px] w-[18px] -translate-y-1/2 text-muted" />
                <input id="login-username" className={fieldCls} autoComplete="username" autoCapitalize="none" spellCheck={false} autoFocus={!username} value={username} onChange={(e) => setUsername(e.target.value)} required />
              </div>
            </div>

            <div>
              <label htmlFor="login-password" className="mb-1.5 block text-sm font-medium text-ink">
                Password
              </label>
              <div className="relative">
                <Lock aria-hidden className="pointer-events-none absolute top-1/2 left-3.5 h-[18px] w-[18px] -translate-y-1/2 text-muted" />
                <input
                  id="login-password"
                  ref={passwordRef}
                  className={cx(fieldCls, 'pr-11')}
                  type={show ? 'text' : 'password'}
                  autoComplete="current-password"
                  autoFocus={!!username}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  onKeyUp={caps}
                  onKeyDown={caps}
                  aria-describedby={capsLock ? 'login-caps' : undefined}
                  required
                />
                <button
                  type="button"
                  onClick={() => setShow((s) => !s)}
                  aria-label={show ? 'Hide characters' : 'Show characters'}
                  aria-controls="login-password"
                  aria-pressed={show}
                  className="absolute top-1/2 right-1.5 flex h-8 w-8 -translate-y-1/2 items-center justify-center rounded-lg text-muted hover:bg-surface-2 hover:text-ink"
                >
                  {show ? <EyeOff aria-hidden className="h-[18px] w-[18px]" /> : <Eye aria-hidden className="h-[18px] w-[18px]" />}
                </button>
              </div>
              {capsLock && (
                <p id="login-caps" className="mt-1.5 flex items-center gap-1.5 text-xs font-medium text-warn">
                  <AlertTriangle aria-hidden className="h-3.5 w-3.5" /> Caps Lock is on
                </p>
              )}
            </div>

            <button
              type="submit"
              disabled={busy}
              className="flex h-11 w-full items-center justify-center gap-2 rounded-xl bg-accent-fill text-[15px] font-semibold text-accent-ink shadow-sm transition hover:opacity-95 focus:outline-none focus-visible:ring-4 focus-visible:ring-accent/30 disabled:cursor-not-allowed disabled:opacity-70"
            >
              {busy && <Loader2 aria-hidden className="h-4 w-4 animate-spin" />}
              Sign in
            </button>
          </form>

          <p className="mt-8 flex items-center justify-center gap-2 border-t border-line pt-6 text-xs text-muted">
            <ShieldCheck aria-hidden className="h-4 w-4 text-good" />
            LockLens only reads from your databases.
          </p>
        </div>
      </section>
    </main>
  )
}
