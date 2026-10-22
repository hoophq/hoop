import { useEffect, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { useLiveEvent } from '@/hooks/useLiveEvent'
import { LIVE_EVENTS } from '@/services/liveEvents'
import { reviewsService } from '@/services/reviews'
import { useUserStore } from '@/stores/useUserStore'
import { showSnackbar } from '@/utils/snackbar'
import { canAskPermission, describeReview, isAttended, isVisible, notificationsGranted } from './helpers'

export const REVIEW_NOTIFICATIONS_FLAG = 'experimental.review_notifications'

const SEEN_CHANNEL = 'hoop-approval-seen'
// How long a tab nobody looks at waits for an attended tab to claim the event.
const HANDOFF_MS = 300
const TOAST_MS = 20000

export default function ApprovalNotifications() {
  const enabled = useUserStore((s) => Boolean(s.featureFlags?.[REVIEW_NOTIFICATIONS_FLAG]))
  const navigate = useNavigate()
  const tabs = useRef(null)
  const seen = useRef(new Set())

  useEffect(() => {
    if (!enabled || typeof BroadcastChannel === 'undefined') return undefined
    const channel = new BroadcastChannel(SEEN_CHANNEL)
    channel.onmessage = (message) => seen.current.add(message.data)
    tabs.current = channel
    return () => {
      channel.close()
      tabs.current = null
    }
  }, [enabled])

  useLiveEvent(
    LIVE_EVENTS.REVIEW_CREATED,
    async ({ id }) => {
      const attended = isAttended()
      const visible = isVisible()
      let watched = false
      if (attended) {
        tabs.current?.postMessage(id)
      } else {
        await new Promise((resolve) => setTimeout(resolve, HANDOFF_MS))
        watched = seen.current.has(id)
        if (watched && !visible) return
      }

      let review
      try {
        const { data } = await reviewsService.get(id)
        review = data
      } catch {
        return
      }
      if (review?.status !== 'PENDING') return
      const { title, body } = describeReview(review)
      const open = () => navigate(`/approvals/${encodeURIComponent(review.session)}`)

      // A visible tab without focus, such as one beside a terminal, gets both.
      const notify = !attended && !watched && notificationsGranted()
      if (notify) {
        const notification = new Notification(title, { body, tag: id })
        notification.onclick = () => {
          window.focus()
          open()
          notification.close()
        }
        if (!visible) return
      }
      const actions = [{ label: 'Open', onClick: open }]
      if (canAskPermission()) {
        actions.push({ label: 'Enable desktop notifications', onClick: () => Notification.requestPermission() })
      }
      showSnackbar({ text: title, description: body, actions, duration: TOAST_MS })
    },
    enabled,
  )

  return null
}
