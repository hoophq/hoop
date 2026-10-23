import { useEffect } from 'react'
import { useLocation } from 'react-router-dom'
import { usePendingApprovalsStore } from '@/stores/usePendingApprovalsStore'
import { useUIStore } from '@/stores/useUIStore'

// The collapsed sidebar shows no count, so it reads none.
export function usePendingApprovalsCount() {
  const { pathname, search } = useLocation()
  const collapsed = useUIStore((s) => s.sidebarCollapsed)
  const count = usePendingApprovalsStore((s) => s.count)
  const fetchCount = usePendingApprovalsStore((s) => s.fetchCount)

  useEffect(() => {
    if (!collapsed) fetchCount()
  }, [pathname, search, collapsed, fetchCount])

  useEffect(() => {
    if (collapsed) return
    const onFocus = () => fetchCount()
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [collapsed, fetchCount])

  return count
}
