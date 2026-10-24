import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { api, ApiError, onUnauthorized, setCsrfToken } from '../api/client'
import type { Session } from '../api/types'

interface AuthValue {
  session: Session | null
  loading: boolean
  login: (username: string, password: string) => Promise<void>
  logout: () => Promise<void>
}

const AuthContext = createContext<AuthValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<Session | null>(null)
  const [loading, setLoading] = useState(true)
  const qc = useQueryClient()

  const apply = useCallback((s: Session | null) => {
    setCsrfToken(s?.csrf_token ?? null)
    setSession(s)
  }, [])

  useEffect(() => {
    let cancelled = false
    api<Session>('/api/v1/session')
      .then((s) => !cancelled && apply(s))
      .catch((e: unknown) => {
        if (!cancelled && (!(e instanceof ApiError) || e.status === 401)) apply(null)
      })
      .finally(() => !cancelled && setLoading(false))
    return () => {
      cancelled = true
    }
  }, [apply])

  useEffect(
    () =>
      onUnauthorized(() => {
        apply(null)
        qc.clear()
      }),
    [apply, qc],
  )

  const value = useMemo<AuthValue>(
    () => ({
      session,
      loading,
      login: async (username, password) => {
        const s = await api<Session>('/api/v1/auth/login', { method: 'POST', json: { username, password } })
        apply(s)
      },
      logout: async () => {
        try {
          await api<undefined>('/api/v1/auth/logout', { method: 'POST' })
        } finally {
          apply(null)
          qc.clear()
        }
      },
    }),
    [session, loading, apply, qc],
  )
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthValue {
  const v = useContext(AuthContext)
  if (!v) throw new Error('useAuth outside AuthProvider')
  return v
}
