import { useEffect } from 'react'
import { useUserStore } from '@/stores/useUserStore'

const DEFAULT_MAX_AGE_MS = 60_000

// Re-reads /serverinfo when the tab regains focus, at most once per maxAge.
// Nothing polls while the tab is active. This is how an admin who installed a
// license from the CLI, or started a sidecar in a terminal, sees the change
// without a reload. Mount once, in the shell.
export function useServerInfoRefresh(maxAge = DEFAULT_MAX_AGE_MS) {
  useEffect(() => {
    const refresh = () => {
      if (document.visibilityState !== 'visible') return
      const { serverInfoFetchedAt, refreshServerInfo } = useUserStore.getState()
      if (Date.now() - serverInfoFetchedAt < maxAge) return
      refreshServerInfo()
    }
    window.addEventListener('focus', refresh)
    document.addEventListener('visibilitychange', refresh)
    return () => {
      window.removeEventListener('focus', refresh)
      document.removeEventListener('visibilitychange', refresh)
    }
  }, [maxAge])
}
