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
// With more open than this, the newest toast (in front) offers "Close all".
const CLOSE_ALL_OVER = 3
// The logo on white: the OS draws the app's own icon (the browser's) beside it.
const NOTIFICATION_ICON = '/notification-icon.png'

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
// Bumped on sign-out, account change and disable: an event still in flight
// from before is dropped.
let epoch = 0

function closeAll() {
  const all = open
  open = []
  all.forEach((t) => dismissSnackbar(t.id))
}

function reset() {
  epoch++
  closeAll()
}

useAuthStore.subscribe((state, prev) => {
  if (state.token !== prev.token) reset()
})

function askPermission() {
  Notification.requestPermission().then(() => open.forEach(show))
}

function show(t) {
  const actions = [{ label: 'Open', onClick: t.goToReview }]
  if (canAskPermission()) {
    actions.push({ label: 'Enable desktop notifications', keepOpen: true, onClick: askPermission })
  }
  if (open.length > CLOSE_ALL_OVER && t === open[open.length - 1]) {
    actions.push({ label: `Close all (${open.length})`, onClick: closeAll })
  }
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
    if (!enabled) reset()
  }, [enabled])

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
      const at = epoch
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
      if (at !== epoch || review?.status !== 'PENDING') return
      const { title, body, who, where, kind } = describeReview(review)
      const goToReview = () => navigate(`/approvals/${encodeURIComponent(review.session)}`)

      // The toast always shows, so a hidden tab has it on return. Without
      // focus, the OS notification shows too.
      if (!attended && !watched && notificationsGranted()) {
        const notification = new Notification(title, { body, tag: id, icon: NOTIFICATION_ICON })
        notification.onclick = () => {
          window.focus()
          goToReview()
          notification.close()
        }
      }
      open.push({ title, description: <Summary who={who} where={where} kind={kind} />, goToReview })
      open.forEach(show)
    },
    enabled,
  )

  return null
}
