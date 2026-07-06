import { useEffect, useState } from 'react'

/**
 * useState backed by localStorage — UI preferences (theme, view mode, tree
 * expansion) survive a refresh. Storage failures (private mode, quota) fall
 * back to plain in-memory state; a malformed stored value falls back to the
 * initial value, so callers may trust the type.
 */
export function usePersistedState<T>(key: string, initial: T | (() => T)) {
  const [value, setValue] = useState<T>(() => {
    try {
      const raw = localStorage.getItem(key)
      if (raw !== null) return JSON.parse(raw) as T
    } catch {
      /* private mode or corrupted value */
    }
    return typeof initial === 'function' ? (initial as () => T)() : initial
  })
  useEffect(() => {
    try {
      localStorage.setItem(key, JSON.stringify(value))
    } catch {
      /* ignore */
    }
  }, [key, value])
  return [value, setValue] as const
}
