import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { api, ApiError, onUnauthorized, setCsrfToken } from './api/client'
import type { Session } from './api/types'

interface AuthState {
  session: Session | null
  checking: boolean
  login: (username: string, password: string) => Promise<void>
  logout: () => Promise<void>
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

  const value = useMemo(() => ({ session, checking, login, logout }), [session, checking, login, logout])
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useAuth(): AuthState {
  const v = useContext(Ctx)
  if (!v) throw new Error('useAuth outside AuthProvider')
  return v
}
