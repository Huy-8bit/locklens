import type { ReactElement } from 'react'
import { render } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import axe from 'axe-core'
import { vi } from 'vitest'
import { ScopeProvider } from '../lib/scope'
import { StaticAuthProvider } from '../auth'
import type { Role, Session } from '../api/types'

const PERMS: Record<Role, Session['permissions']> = {
  viewer: ['view', 'workspace.structure'],
  operator: ['view', 'workspace.structure', 'workspace.data', 'workspace.sql', 'incidents.acknowledge'],
  admin: ['view', 'workspace.structure', 'workspace.data', 'workspace.sql', 'incidents.acknowledge', 'targets.manage', 'settings.manage', 'users.manage'],
}

/** A signed-in session with a role, as the server would return it. */
export function sessionFor(role: Role, managed = false): Session {
  return { user_id: 1, username: role === 'admin' ? 'admin' : `${role}-user`, role, managed, permissions: PERMS[role], csrf_token: 'tok', expires_at: '', server: { version: 'test', demo_mode: false, time: '' } }
}

/** Renders an element inside the router, a fresh query client, and a session (admin by default). */
export function renderAt(path: string, routePattern: string, element: ReactElement, session: Session = sessionFor('admin')) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <StaticAuthProvider session={session}>
        <ScopeProvider>
          <MemoryRouter initialEntries={[path]}>
            <Routes>
              <Route path={routePattern} element={element} />
            </Routes>
          </MemoryRouter>
        </ScopeProvider>
      </StaticAuthProvider>
    </QueryClientProvider>,
  )
}

type Handler = (init: RequestInit | undefined, url: URL) => { status?: number; body: unknown }

/** Stubs fetch with handlers keyed by path prefix; returns the recorded calls. */
export function mockFetch(handlers: Record<string, Handler>) {
  const calls: { method: string; url: string; body?: string }[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), 'http://localhost')
      calls.push({ method: init?.method ?? 'GET', url: url.pathname + url.search, body: typeof init?.body === 'string' ? init.body : undefined })
      const key = Object.keys(handlers)
        .sort((a, b) => b.length - a.length)
        .find((k) => url.pathname.startsWith(k))
      if (!key) return new Response(JSON.stringify({ error: { code: 'not_found', message: 'not mocked' } }), { status: 404 })
      const { status = 200, body } = handlers[key]?.(init, url) ?? { body: null }
      return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
    }),
  )
  return calls
}

/** Serious or critical axe-core violations in a container. */
export async function axeViolations(container: Element): Promise<string[]> {
  const r = await axe.run(container, { resultTypes: ['violations'], rules: { 'color-contrast': { enabled: false } } })
  return r.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical').map((v) => `${v.id}: ${v.help}`)
}
