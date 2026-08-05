import { Group, Text } from '@mantine/core'
import Loader from '@/components/PageLoader'
import ResultsContainer from '../../components/ResultsContainer'
import { sanitizeResponse } from '../../components/resultsMatrix'
import { decodeB64 } from '../../decodeB64'

/**
 * Port of the response/event-stream section (session_details.cljs:358-405).
 *
 * v1 hides this block entirely under several conditions — a session whose
 * credentials are still pending, one that is merely `ready`, or one whose
 * review has not been decided — because there is no output to show yet.
 *
 * Only the `exec` branch is implemented here. The `connect` branch renders
 * asciinema / the RDP canvas / the SSE live tail and lands with the playback
 * commit; until then a connect session shows nothing in this slot, which is
 * also what v1 does before its stream arrives.
 */
export default function SessionOutput({ session, detail }) {
  const review = session?.review
  const reviewGroups = review?.review_groups_data ?? []

  const hidden =
    Boolean(session?.metadata?.credentials_expire_at) ||
    session?.status === 'ready' ||
    (review?.status === 'PENDING' && reviewGroups.some((g) => g.status === 'PENDING'))

  if (hidden) return null

  if (detail.status === 'loading') {
    return (
      <Group gap="xs" align="center">
        <Text size="xs" fs="italic" c="dimmed">
          Loading data for this session
        </Text>
        <Loader h={24} />
      </Group>
    )
  }

  if (session?.verb !== 'exec') return null

  const { hasLargePayload } = detail
  const streamData = detail.streamResult?.data
  const streamStatus = detail.streamResult?.status

  // Oversized payloads come from /result/stream instead of the session body.
  if (hasLargePayload && streamStatus === 'loading') {
    return (
      <Group gap="xs" align="center">
        <Text size="xs" fs="italic" c="dimmed">
          Loading data for this session
        </Text>
        <Loader h={24} />
      </Group>
    )
  }

  const raw = hasLargePayload
    ? streamData
    : decodeB64(session.event_stream?.[0] ?? '')

  const results = sanitizeResponse(raw, session.connection_subtype)
  const resultsStatus = hasLargePayload ? (streamData ? 'success' : 'error') : 'success'

  return (
    <ResultsContainer
      connectionSubtype={session.connection_subtype}
      results={results}
      resultsStatus={resultsStatus}
      fixedHeight
      sessionId={session.id}
      connectionName={session.connection}
      hasLargePayload={hasLargePayload}
    />
  )
}
