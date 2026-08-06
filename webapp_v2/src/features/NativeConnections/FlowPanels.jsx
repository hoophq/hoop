import { Group, Loader, Stack, Text } from '@mantine/core'
import { Clock, TriangleAlert } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import { useNativeAccessStore } from '@/stores/useNativeAccessStore'
import { useNativeConnectionsStore } from '@/stores/useNativeConnectionsStore'

/** Agent offline, or the connection lookup / credential request failed. */
export function UnavailablePanel({ message }) {
  return (
    <Alert color="amber" icon={<TriangleAlert size={16} />} title="Connection method not available">
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
export function PendingReviewPanel({ connectionName, sessionId, accessDurationSec }) {
  const resumeAfterReview = useNativeAccessStore((s) => s.resumeAfterReview)
  const closeDrawer = useNativeConnectionsStore((s) => s.close)

  // Full navigation rather than a React Router <Link>. The session page is
  // ClojureScript, and the legacy router only dispatches a panel keyword —
  // webapp/src/webapp/core.cljs hoopSetRoute and routes.cljs dispatch both drop
  // the route params. Panels read their id straight off window.location at
  // render time, so going from /sessions/A to /sessions/B leaves :active-panel
  // unchanged, nothing re-renders, and the old session stays on screen. That is
  // a pre-existing limitation of the legacy router (untouched by this branch);
  // a reload is the one transition it always gets right. Remove this once the
  // session page is React.
  const viewReview = () => {
    closeDrawer()
    window.location.assign(`/sessions/${sessionId}`)
  }

  return (
    <Stack gap="sm">
      <Alert color="amber" icon={<Clock size={16} />} title="Waiting for review approval">
        A reviewer has to approve this request before the credentials are issued. This row picks
        them up on its own once that happens.
      </Alert>
      {sessionId && (
        <Group justify="flex-end" gap="sm">
          <Button variant="default" size="sm" onClick={viewReview}>
            View review
          </Button>
          <Button
            size="sm"
            onClick={() => resumeAfterReview(connectionName, sessionId, accessDurationSec)}
          >
            Check approval
          </Button>
        </Group>
      )}
    </Stack>
  )
}
