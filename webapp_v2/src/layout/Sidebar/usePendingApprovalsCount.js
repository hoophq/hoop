import { useEffect } from 'react'
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

  // One read each time the user comes back. A tab switch fires both focus and
  // visibilitychange; a window uncovered without focus fires only the latter.
  useEffect(() => {
    let away = document.hidden || !document.hasFocus()
    const leave = () => {
      away = true
    }
    const back = () => {
      if (!away) return
      away = false
      fetchCount()
    }
    const onVisibility = () => (document.hidden ? leave() : back())
    window.addEventListener('blur', leave)
    window.addEventListener('focus', back)
    document.addEventListener('visibilitychange', onVisibility)
    return () => {
      window.removeEventListener('blur', leave)
      window.removeEventListener('focus', back)
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [fetchCount])

  useLiveEvent(
    LIVE_EVENTS.REVIEW_CREATED,
    () => {
      // A hidden tab skips it: the read on return catches up.
      if (!document.hidden) fetchCount()
    },
    live,
  )

  return count
}
