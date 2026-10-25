import { useEffect, useState } from 'react'

export type Theme = 'light' | 'dark'

const KEY = 'locklens-theme'

function initial(): Theme {
  try {
    const v = localStorage.getItem(KEY)
    if (v === 'light' || v === 'dark') return v
  } catch {
    // Storage can be unavailable (private mode); fall back to the OS setting.
  }
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

/** Theme preference (a per-viewer convenience; nothing else is stored). */
export function useTheme(): [Theme, () => void] {
  const [theme, setTheme] = useState<Theme>(initial)
  useEffect(() => {
    document.documentElement.dataset.theme = theme
    try {
      localStorage.setItem(KEY, theme)
    } catch {
      // ignore
    }
    window.dispatchEvent(new Event('locklens-theme'))
  }, [theme])
  return [theme, () => setTheme((t) => (t === 'dark' ? 'light' : 'dark'))]
}

/** Re-renders when the theme changes (charts read resolved CSS colors). */
export function useThemeVersion(): number {
  const [v, setV] = useState(0)
  useEffect(() => {
    const bump = () => setV((x) => x + 1)
    window.addEventListener('locklens-theme', bump)
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    mq.addEventListener('change', bump)
    return () => {
      window.removeEventListener('locklens-theme', bump)
      mq.removeEventListener('change', bump)
    }
  }, [])
  return v
}
