// Formatting helpers. Every function renders null/undefined as an explicit
// "unknown" marker (or a supplied label) and never as zero.

export const UNKNOWN = 'unknown'

export function isKnown(v: number | null | undefined): v is number {
  return typeof v === 'number' && Number.isFinite(v)
}

export function fmtNumber(v: number | null | undefined, digits = 0, empty = UNKNOWN): string {
  if (!isKnown(v)) return empty
  return v.toLocaleString('en-US', { maximumFractionDigits: digits, minimumFractionDigits: 0 })
}

export function fmtRate(v: number | null | undefined, unit = '/s', empty = UNKNOWN): string {
  if (!isKnown(v)) return empty
  if (v > 0 && v < 0.01) return `<0.01${unit}`
  const digits = Math.abs(v) < 10 ? 2 : Math.abs(v) < 100 ? 1 : 0
  return `${v.toLocaleString('en-US', { maximumFractionDigits: digits })}${unit}`
}

export function fmtPercent(ratio: number | null | undefined, empty = UNKNOWN): string {
  if (!isKnown(ratio)) return empty
  const p = ratio * 100
  return `${p.toLocaleString('en-US', { maximumFractionDigits: p < 10 ? 1 : 0 })}%`
}

export function fmtBytes(v: number | null | undefined, empty = UNKNOWN): string {
  if (!isKnown(v)) return empty
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  let n = Math.abs(v)
  let i = 0
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  const sign = v < 0 ? '-' : ''
  return `${sign}${n.toLocaleString('en-US', { maximumFractionDigits: n < 10 ? 1 : 0 })} ${units[i]}`
}

export function fmtDuration(seconds: number | null | undefined, empty = UNKNOWN): string {
  if (!isKnown(seconds)) return empty
  const s = Math.max(0, seconds)
  if (s < 1) return `${Math.round(s * 1000)} ms`
  if (s < 60) return `${s < 10 ? s.toFixed(1) : Math.round(s)} s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${String(Math.floor(s % 60)).padStart(2, '0')}s`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ${String(m % 60).padStart(2, '0')}m`
  return `${Math.floor(h / 24)}d ${String(h % 24).padStart(2, '0')}h`
}

export function fmtMs(v: number | null | undefined, empty = UNKNOWN): string {
  if (!isKnown(v)) return empty
  if (v < 1) return `${v.toFixed(3)} ms`
  if (v < 1000) return `${v.toFixed(v < 10 ? 2 : 1)} ms`
  return fmtDuration(v / 1000)
}

export type TimeZoneMode = 'local' | 'utc'

let tzMode: TimeZoneMode = 'local'
export function setTimeZoneMode(m: TimeZoneMode): void {
  tzMode = m
}
export function getTimeZoneMode(): TimeZoneMode {
  return tzMode
}

export function timeZoneLabel(mode: TimeZoneMode = tzMode): string {
  if (mode === 'utc') return 'UTC'
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone
  const offset = -new Date().getTimezoneOffset()
  const sign = offset >= 0 ? '+' : '-'
  const hh = String(Math.floor(Math.abs(offset) / 60)).padStart(2, '0')
  const mm = String(Math.abs(offset) % 60).padStart(2, '0')
  return `${tz} (UTC${sign}${hh}:${mm})`
}

export function fmtTime(iso: string | null | undefined, opts: { seconds?: boolean; date?: boolean } = {}, empty = '—'): string {
  if (!iso) return empty
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return empty
  const o: Intl.DateTimeFormatOptions = {
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
    timeZone: tzMode === 'utc' ? 'UTC' : undefined,
  }
  if (opts.seconds !== false) o.second = '2-digit'
  if (opts.date !== false) {
    o.year = 'numeric'
    o.month = '2-digit'
    o.day = '2-digit'
  }
  return new Intl.DateTimeFormat('en-CA', o).format(d).replace(',', '')
}

export function fmtAgo(iso: string | null | undefined, now = Date.now(), empty = 'never'): string {
  if (!iso) return empty
  const s = (now - new Date(iso).getTime()) / 1000
  return `${fmtDuration(s)} ago`
}

/** Formats a value given a unit hint used by evidence and charts. */
export function fmtUnit(v: unknown, unit?: string): string {
  if (v === null || v === undefined) return UNKNOWN
  if (typeof v === 'number') {
    switch (unit) {
      case 's':
        return fmtDuration(v)
      case 'ms':
        return fmtMs(v)
      case 'bytes':
        return fmtBytes(v)
      case 'ratio':
        return fmtPercent(v)
      default:
        return `${fmtNumber(v, 3)}${unit ? ` ${unit}` : ''}`
    }
  }
  if (typeof v === 'boolean') return v ? 'yes' : 'no'
  if (Array.isArray(v)) return v.join(', ')
  if (typeof v === 'string') return v
  return JSON.stringify(v)
}
