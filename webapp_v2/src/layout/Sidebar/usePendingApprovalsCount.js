import { useEffect } from 'react'
import { useLocation } from 'react-router-dom'
import { usePendingApprovalsStore } from '@/stores/usePendingApprovalsStore'
import { useUIStore } from '@/stores/useUIStore'

export function usePendingApprovalsCount() {
  const { pathname, search } = useLocation()
  // Expanding is a trigger too: the store reads nothing while collapsed.
  const collapsed = useUIStore((s) => s.sidebarCollapsed)
  const count = usePendingApprovalsStore((s) => s.count)
  const fetchCount = usePendingApprovalsStore((s) => s.fetchCount)

  useEffect(() => {
    fetchCount()
  }, [pathname, search, collapsed, fetchCount])

  useEffect(() => {
    const onFocus = () => fetchCount()
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [fetchCount])

  return count
}
