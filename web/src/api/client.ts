// Minimal fetch wrapper. Authentication uses the HttpOnly session cookie set
// by the server; the CSRF token is kept in memory only (never in
// localStorage) and sent on every mutation.

let csrfToken: string | null = null

export function setCsrfToken(token: string | null): void {
  csrfToken = token
}

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly fields: Record<string, string>
  readonly requestId: string

  constructor(status: number, code: string, message: string, fields: Record<string, string> = {}, requestId = '') {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.fields = fields
    this.requestId = requestId
  }
}

type Listener = () => void
const unauthorizedListeners = new Set<Listener>()

/** Registers a callback for 401 responses (session expired). */
export function onUnauthorized(fn: Listener): () => void {
  unauthorizedListeners.add(fn)
  return () => unauthorizedListeners.delete(fn)
}

interface ErrorBody {
  error?: { code?: string; message?: string; fields?: Record<string, string>; request_id?: string }
}

export async function api<T>(path: string, init: RequestInit & { json?: unknown } = {}): Promise<T> {
  const headers = new Headers(init.headers)
  const method = (init.method ?? 'GET').toUpperCase()
  let body = init.body
  if (init.json !== undefined) {
    headers.set('Content-Type', 'application/json')
    body = JSON.stringify(init.json)
  }
  if (method !== 'GET' && method !== 'HEAD' && csrfToken) {
    headers.set('X-CSRF-Token', csrfToken)
  }
  let res: Response
  try {
    res = await fetch(path, { ...init, method, headers, body, credentials: 'same-origin' })
  } catch {
    throw new ApiError(0, 'network', 'Cannot reach the LockLens server.')
  }
  if (res.status === 204) {
    return undefined as T
  }
  const text = await res.text()
  let parsed: unknown = undefined
  if (text) {
    try {
      parsed = JSON.parse(text)
    } catch {
      parsed = undefined
    }
  }
  if (!res.ok) {
    const e = (parsed as ErrorBody | undefined)?.error
    if (res.status === 401 && !path.endsWith('/auth/login')) {
      unauthorizedListeners.forEach((fn) => fn())
    }
    throw new ApiError(res.status, e?.code ?? 'http_error', e?.message ?? `Request failed with HTTP ${res.status}`, e?.fields ?? {}, e?.request_id ?? '')
  }
  return parsed as T
}

/** Builds a query string, skipping empty values. */
export function qs(params: Record<string, string | number | boolean | undefined | null>): string {
  const u = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== '') {
      u.set(k, String(v))
    }
  }
  const s = u.toString()
  return s ? `?${s}` : ''
}
