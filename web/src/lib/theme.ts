import { useEffect, useState } from 'react'
import { setTimeZoneMode, type TimeZoneMode } from './format'

export type Theme = 'light' | 'dark'

function read(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

function write(key: string, value: string): void {
  try {
    localStorage.setItem(key, value)
  } catch {
    // Preferences are optional; private browsing may block storage.
  }
}

/** Theme preference: stored choice, else the system preference. */
export function useTheme(): [Theme, (t: Theme) => void] {
  const [theme, setTheme] = useState<Theme>(() => {
    const stored = read('locklens.theme')
    if (stored === 'light' || stored === 'dark') return stored
    return typeof window !== 'undefined' && window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
  })
  useEffect(() => {
    document.documentElement.dataset.theme = theme
  }, [theme])
  return [
    theme,
    (t) => {
      write('locklens.theme', t)
      setTheme(t)
    },
  ]
}

/** Time zone display preference (timestamps are always stored in UTC). */
export function useTimeZone(): [TimeZoneMode, (m: TimeZoneMode) => void] {
  const [mode, setMode] = useState<TimeZoneMode>(() => {
    const m = read('locklens.tz') === 'utc' ? 'utc' : 'local'
    setTimeZoneMode(m)
    return m
  })
  return [
    mode,
    (m) => {
      write('locklens.tz', m)
      setTimeZoneMode(m)
      setMode(m)
    },
  ]
}
