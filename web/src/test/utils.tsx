import type { ReactElement } from 'react'
import { render } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import axe from 'axe-core'
import { vi } from 'vitest'

/** Renders an element inside the router and a fresh query client. */
export function renderAt(path: string, routePattern: string, element: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path={routePattern} element={element} />
        </Routes>
      </MemoryRouter>
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
