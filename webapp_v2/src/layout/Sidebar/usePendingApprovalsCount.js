import { useEffect, useRef } from 'react'
import { useLocation } from 'react-router-dom'
import { REVIEW_NOTIFICATIONS_FLAG } from '@/features/ApprovalNotifications'
import { useLiveEvent } from '@/hooks/useLiveEvent'
import { LIVE_EVENTS } from '@/services/liveEvents'
import { usePendingApprovalsStore } from '@/stores/usePendingApprovalsStore'
import { useUIStore } from '@/stores/useUIStore'
import { useUserStore } from '@/stores/useUserStore'

export function usePendingApprovalsCount() {
  const { pathname, search } = useLocation()
  // Expanding is a trigger too: the store reads nothing while collapsed.
  const collapsed = useUIStore((s) => s.sidebarCollapsed)
  const count = usePendingApprovalsStore((s) => s.count)
  const fetchCount = usePendingApprovalsStore((s) => s.fetchCount)
  // The gateway sends review.created only while this flag is on.
  const live = useUserStore((s) => Boolean(s.featureFlags?.[REVIEW_NOTIFICATIONS_FLAG]))

  useEffect(() => {
    fetchCount()
  }, [pathname, search, collapsed, fetchCount])

  useEffect(() => {
    const onFocus = () => fetchCount()
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [fetchCount])

  // A hidden tab reads once when shown again, not once per event.
  const stale = useRef(false)

  useLiveEvent(
    LIVE_EVENTS.REVIEW_CREATED,
    () => {
      if (document.hidden) stale.current = true
      else fetchCount()
    },
    live,
  )

  useEffect(() => {
    const onVisible = () => {
      if (document.hidden || !stale.current) return
      stale.current = false
      fetchCount()
    }
    document.addEventListener('visibilitychange', onVisible)
    return () => document.removeEventListener('visibilitychange', onVisible)
  }, [fetchCount])

  return count
}
