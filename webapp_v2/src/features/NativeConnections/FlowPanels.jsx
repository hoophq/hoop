import { Group, Loader, Stack, Text } from '@mantine/core'
import { Clock, TriangleAlert } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import { useNativeAccessStore } from '@/stores/useNativeAccessStore'
import { useNativeConnectionsStore } from '@/stores/useNativeConnectionsStore'
import classes from './NativeConnections.module.css'

/** Agent offline, or the connection lookup / credential request failed. */
export function UnavailablePanel({ message }) {
  return (
    <Alert color="amber" icon={<TriangleAlert size={16} />} title="Connection method not available" classNames={{ title: classes.amberText, icon: classes.amberText }}>
      {message}
    </Alert>
  )
}

/** Credential request in flight. */
export function RequestingPanel({ connectionName }) {
  return (
    <Group gap="sm" py="md">
      <Loader size="sm" />
      <Stack gap={0}>
        <Text fz="sm" fw={600}>
          {`Connecting to ${connectionName}`}
        </Text>
        <Text fz="xs" c="dimmed">
          Generating native client credentials...
        </Text>
      </Stack>
    </Group>
  )
}

/**
 * The 202 branch: a review was opened and no credential exists yet.
 *
 * The CLJS flow navigated away into the session-details modal here. Keeping the
 * state inline means the user does not lose the drawer or the page they were on.
 *
 * There is no push channel for an approval, so the store polls the resume
 * endpoint while this is on screen (syncReviewWatcher) and swaps this panel for
 * the credentials — and the row for its countdown — as soon as it goes through.
 * "Check approval" is the same call on demand, for anyone who does not want to
 * wait out the interval.
 */
export function PendingReviewPanel({
  connectionName,
  sessionId,
  accessDurationSec,
  checking,
  hint,
}) {
  const navigate = useNavigate()
  const resumeAfterReview = useNativeAccessStore((s) => s.resumeAfterReview)
  const closeDrawer = useNativeConnectionsStore((s) => s.close)

  // The requester sees their own request on /approvals (owner visibility in
  // gateway/models/reviews.go), so the button opens the approval itself.
  const viewReview = () => {
    closeDrawer()
    navigate(`/approvals/${encodeURIComponent(sessionId)}`)
  }

  return (
    <Stack gap="sm">
      <Alert color="amber" icon={<Clock size={16} />} title="Waiting for approval" classNames={{ title: classes.amberText, icon: classes.amberText }}>
        The credentials are issued once this request is approved. This row picks them up on its
        own.
      </Alert>
      {/* A failed check does not end the wait — the review is still open — so
          the message is a hint here rather than an error panel that would take
          the actions away with it. */}
      {hint && (
        <Text fz="xs" c="dimmed">
          {`Last check failed: ${hint}`}
        </Text>
      )}
      {sessionId && (
        <Group justify="flex-end" gap="sm">
          <Button variant="default" size="sm" onClick={viewReview}>
            View approval request
          </Button>
          <Button
            size="sm"
            loading={checking}
            onClick={() => resumeAfterReview(connectionName, sessionId, accessDurationSec)}
          >
            Check approval
          </Button>
        </Group>
      )}
    </Stack>
  )
}
