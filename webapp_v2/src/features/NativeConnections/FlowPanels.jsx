import { Group, Loader, Stack, Text } from '@mantine/core'
import { Clock, TriangleAlert } from 'lucide-react'
import { Link } from 'react-router-dom'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import { useNativeAccessStore } from '@/stores/useNativeAccessStore'

/** Agent offline, or the connection lookup / credential request failed. */
export function UnavailablePanel({ message }) {
  return (
    <Alert color="orange" icon={<TriangleAlert size={16} />} title="Connection method not available">
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
 * There is no push channel for an approval, so the row cannot update itself —
 * "Check approval" re-issues the request, which returns the credentials once a
 * reviewer has approved and another 202 until then. The CLJS session page can
 * also drive this through the hoop:native-access-resume bridge event.
 */
export function PendingReviewPanel({ connectionName, sessionId, accessDurationSec }) {
  const resumeAfterReview = useNativeAccessStore((s) => s.resumeAfterReview)

  return (
    <Stack gap="sm">
      <Alert color="blue" icon={<Clock size={16} />} title="Waiting for review approval">
        A reviewer has to approve this request before the credentials are issued.
      </Alert>
      <Group gap="sm">
        {sessionId && (
          <Button
            onClick={() => resumeAfterReview(connectionName, sessionId, accessDurationSec)}
            size="xs"
          >
            Check approval
          </Button>
        )}
        {sessionId && (
          <Button component={Link} to={`/sessions/${sessionId}`} variant="light" size="xs">
            View review
          </Button>
        )}
      </Group>
    </Stack>
  )
}
