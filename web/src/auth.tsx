import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { api, ApiError, onUnauthorized, setCsrfToken } from './api/client'
import type { Permission, Role, Session } from './api/types'

interface AuthState {
  session: Session | null
  checking: boolean
  login: (username: string, password: string) => Promise<void>
  logout: () => Promise<void>
  /** Replaces the session (for example after a password change). */
  replace: (s: Session) => void
}

const Ctx = createContext<AuthState | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient()
  const [session, setSession] = useState<Session | null>(null)
  const [checking, setChecking] = useState(true)

  useEffect(() => {
    api<Session>('/api/v1/session')
      .then((s) => {
        setCsrfToken(s.csrf_token)
        setSession(s)
      })
      .catch(() => setSession(null))
      .finally(() => setChecking(false))
    return onUnauthorized(() => {
      setCsrfToken(null)
      setSession(null)
    })
  }, [])

  const login = useCallback(async (username: string, password: string) => {
    const s = await api<Session>('/api/v1/auth/login', { method: 'POST', json: { username, password } })
    setCsrfToken(s.csrf_token)
    setSession(s)
  }, [])

  const logout = useCallback(async () => {
    try {
      await api<undefined>('/api/v1/auth/logout', { method: 'POST' })
    } catch (e) {
      if (!(e instanceof ApiError) || e.status !== 401) throw e
    }
    setCsrfToken(null)
    setSession(null)
    qc.clear()
  }, [qc])

  const replace = useCallback((s: Session) => {
    setCsrfToken(s.csrf_token)
    setSession(s)
  }, [])

  const value = useMemo(() => ({ session, checking, login, logout, replace }), [session, checking, login, logout, replace])
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useAuth(): AuthState {
  const v = useContext(Ctx)
  if (!v) throw new Error('useAuth outside AuthProvider')
  return v
}

/** A fixed session, for tests and previews. */
export function StaticAuthProvider({ session, children }: { session: Session; children: ReactNode }) {
  const value = useMemo<AuthState>(() => ({ session, checking: false, login: async () => {}, logout: async () => {}, replace: () => {} }), [session])
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export const ROLE_LABEL: Record<Role, string> = { admin: 'Administrator', operator: 'Operator', viewer: 'Viewer' }

/** Whether the signed-in user has a permission. The server enforces it too. */
export function useCan(): (p: Permission) => boolean {
  const { session } = useAuth()
  return useCallback((p: Permission) => !!session?.permissions?.includes(p), [session])
}

