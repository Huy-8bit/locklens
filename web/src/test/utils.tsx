import type { ReactElement } from 'react'
import { render } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import axe from 'axe-core'

export function renderWithProviders(ui: ReactElement, { route = '/' }: { route?: string } = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[route]}>{ui}</MemoryRouter>
    </QueryClientProvider>,
  )
}

/** Runs axe-core on a container and returns violation IDs (color contrast is not computable in jsdom). */
export async function axeViolations(container: Element): Promise<string[]> {
  const res = await axe.run(container, { rules: { 'color-contrast': { enabled: false }, region: { enabled: false } } })
  return res.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`)
}

/** Replaces fetch with a router of canned JSON responses (unit tests only). */
export function mockFetch(routes: Record<string, (init?: RequestInit) => { status?: number; body?: unknown }>) {
  const calls: { url: string; init?: RequestInit }[] = []
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    calls.push({ url, init })
    const key = Object.keys(routes).find((k) => url.startsWith(k))
    const r = key ? routes[key]?.(init) : { status: 404, body: { error: { code: 'not_found', message: 'not mocked' } } }
    const status = r?.status ?? 200
    return new Response(status === 204 ? null : JSON.stringify(r?.body ?? {}), { status, headers: { 'Content-Type': 'application/json' } })
  }) as typeof fetch
  return calls
}
