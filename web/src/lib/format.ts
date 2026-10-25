// Formatting helpers. null/undefined always render as "—" (unknown), never 0.

export const UNKNOWN = '—'

type N = number | null | undefined

const known = (v: N): v is number => v !== null && v !== undefined && Number.isFinite(v)

export function num(v: N, digits = 0): string {
  if (!known(v)) return UNKNOWN
  return v.toLocaleString('en-US', { maximumFractionDigits: digits, minimumFractionDigits: 0 })
}

/** Compact count: 1.2k, 3.4M. */
export function compact(v: N): string {
  if (!known(v)) return UNKNOWN
  const a = Math.abs(v)
  if (a >= 1e9) return `${(v / 1e9).toFixed(a >= 1e10 ? 0 : 1)}B`
  if (a >= 1e6) return `${(v / 1e6).toFixed(a >= 1e7 ? 0 : 1)}M`
  if (a >= 1e4) return `${(v / 1e3).toFixed(0)}k`
  if (a >= 1e3) return `${(v / 1e3).toFixed(1)}k`
  return num(v, a < 10 ? 2 : a < 100 ? 1 : 0)
}

/** Per-second rate with a unit suffix. */
export function rate(v: N, unit = '/s'): string {
  if (!known(v)) return UNKNOWN
  if (v > 0 && v < 0.01) return `<0.01${unit}`
  return `${compact(v)}${unit}`
}

export function pct(v: N, digits = 1): string {
  if (!known(v)) return UNKNOWN
  return `${(v * 100).toFixed(digits)}%`
}

export function bytes(v: N): string {
  if (!known(v)) return UNKNOWN
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let x = Math.abs(v)
  let i = 0
  while (x >= 1024 && i < units.length - 1) {
    x /= 1024
    i++
  }
  return `${v < 0 ? '-' : ''}${x.toFixed(x >= 100 || i === 0 ? 0 : 1)} ${units[i]}`
}

/** Milliseconds as a human duration (0.42 ms, 12 ms, 1.4 s, 3m 2s). */
export function ms(v: N): string {
  if (!known(v)) return UNKNOWN
  if (v < 1) return `${v.toFixed(2)} ms`
  if (v < 1000) return `${v.toFixed(v < 10 ? 1 : 0)} ms`
  return seconds(v / 1000)
}

export function seconds(v: N): string {
  if (!known(v)) return UNKNOWN
  if (v < 1) return `${Math.round(v * 1000)} ms`
  if (v < 60) return `${v.toFixed(v < 10 ? 1 : 0)} s`
  const m = Math.floor(v / 60)
  if (m < 60) return `${m}m ${Math.floor(v % 60)}s`
  const h = Math.floor(m / 60)
  if (h < 48) return `${h}h ${m % 60}m`
  return `${Math.floor(h / 24)}d ${h % 24}h`
}

export function ago(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return 'never'
  const s = (now - Date.parse(iso)) / 1000
  if (!Number.isFinite(s)) return UNKNOWN
  if (s < 2) return 'just now'
  return `${seconds(Math.max(0, s))} ago`
}

export function time(iso: string | null | undefined, opts: { date?: boolean; seconds?: boolean } = {}): string {
  if (!iso) return UNKNOWN
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return UNKNOWN
  const t = d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit', second: opts.seconds === false ? undefined : '2-digit' })
  if (!opts.date) return t
  return `${d.toLocaleDateString('en-CA')} ${t}`
}

/** Age in seconds of an ISO timestamp relative to a reference ISO time. */
export function ageOf(iso: string | null | undefined, ref: string | null | undefined): number | null {
  if (!iso || !ref) return null
  const a = Date.parse(ref) - Date.parse(iso)
  return Number.isFinite(a) ? Math.max(0, a / 1000) : null
}
