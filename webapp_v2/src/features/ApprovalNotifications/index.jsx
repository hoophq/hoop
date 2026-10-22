import { useEffect, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { Group, Stack, Text } from '@mantine/core'
import Badge from '@/components/Badge'
import { useLiveEvent } from '@/hooks/useLiveEvent'
import { LIVE_EVENTS } from '@/services/liveEvents'
import { reviewsService } from '@/services/reviews'
import { useAuthStore } from '@/stores/useAuthStore'
import { useUserStore } from '@/stores/useUserStore'
import { dismissSnackbar, showSnackbar } from '@/utils/snackbar'
import { canAskPermission, describeReview, isAttended, isVisible, notificationsGranted } from './helpers'

export const REVIEW_NOTIFICATIONS_FLAG = 'experimental.review_notifications'

const SEEN_CHANNEL = 'hoop-approval-seen'
// How long a tab nobody looks at waits for an attended tab to claim the event.
const HANDOFF_MS = 300
// With more open than this, each toast offers "Close all".
const CLOSE_ALL_OVER = 2

function Summary({ who, where, kind }) {
  return (
    <Stack gap={6} mt={4}>
      {who && (
        <Text size="sm" fw={500}>
          {who}
        </Text>
      )}
      <Group gap={6}>
        {where.map((name) => (
          <Badge key={name} variant="light" color="gray" tag>
            {name}
          </Badge>
        ))}
        {kind && (
          <Text size="xs" c="dimmed">
            {kind}
          </Text>
        )}
      </Group>
    </Stack>
  )
}

// Outside the component: the layout that mounts it remounts on every move
// between a React and a CLJS page.
let open = []

function closeAll() {
  const all = open
  open = []
  all.forEach((t) => dismissSnackbar(t.id))
}

useAuthStore.subscribe((state, prev) => {
  if (prev.isAuthenticated && !state.isAuthenticated) closeAll()
})

function show(t) {
  const actions =
    open.length > CLOSE_ALL_OVER ? [...t.actions, { label: `Close all (${open.length})`, onClick: closeAll }] : t.actions
  t.id = showSnackbar({
    id: t.id,
    level: 'approval',
    text: t.title,
    description: t.description,
    actions,
    duration: Infinity,
    onDismiss: () => {
      open = open.filter((o) => o !== t)
      // sonner's Toaster resubscribes while onDismiss runs: an update sent now is lost.
      setTimeout(() => open.forEach(show))
    },
  })
}

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
      const { title, body, who, where, kind } = describeReview(review)
      const goToReview = () => navigate(`/approvals/${encodeURIComponent(review.session)}`)

      // A visible tab without focus, such as one beside a terminal, gets both.
      const notify = !attended && !watched && notificationsGranted()
      if (notify) {
        const notification = new Notification(title, { body, tag: id })
        notification.onclick = () => {
          window.focus()
          goToReview()
          notification.close()
        }
        if (!visible) return
      }
      const actions = [{ label: 'Open', onClick: goToReview }]
      if (canAskPermission()) {
        actions.push({ label: 'Enable desktop notifications', onClick: () => Notification.requestPermission() })
      }
      open.push({ title, description: <Summary who={who} where={where} kind={kind} />, actions })
      open.forEach(show)
    },
    enabled,
  )

  return null
}
