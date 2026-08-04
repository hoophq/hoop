import { Group, Loader, Stack, Text } from '@mantine/core'
import { Clock, TriangleAlert } from 'lucide-react'
import { Link } from 'react-router-dom'
import Alert from '@/components/Alert'
import Button from '@/components/Button'

/** Agent offline, or the connection lookup / credential request failed. */
export function UnavailablePanel({ message }) {
  return (
    <Alert color="orange" icon={<TriangleAlert size={16} />} title="Connection method not available">
      {message}
    </Alert>
  )
}

/**
 * Reached after a disconnect or an expiry while the row is still open.
 *
 * Reconnecting is explicit rather than automatic: the row stays expanded, so
 * anything that fired the flow off a "no status yet" condition would silently
 * undo the disconnect the user just confirmed.
 */
export function IdlePanel({ onConnect }) {
  return (
    <Stack gap="sm" align="flex-start">
      <Text fz="sm" c="dimmed">
        No active session for this role.
      </Text>
      <Button size="xs" onClick={onConnect}>
        Connect
      </Button>
    </Stack>
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
 * state inline means the user does not lose the drawer or the page they were
 * on, and the row picks the credential up automatically once approved.
 */
export function PendingReviewPanel({ sessionId }) {
  return (
    <Stack gap="sm">
      <Alert color="blue" icon={<Clock size={16} />} title="Waiting for review approval">
        A reviewer has to approve this request before the credentials are issued. This row updates
        as soon as it is approved.
      </Alert>
      {sessionId && (
        <Button
          component={Link}
          to={`/sessions/${sessionId}`}
          variant="light"
          size="xs"
          w="fit-content"
        >
          View review
        </Button>
      )}
    </Stack>
  )
}
