import { Stack } from '@mantine/core'
import Modal from '@/components/Modal'
import PageLoader from '@/components/PageLoader'
import { useSessionsStore } from '../store'
import SessionInfo from './sections/SessionInfo'
import ReviewActions from './sections/ReviewActions'

/**
 * Port of the v1 session-details modal (`audit/views/session_details.cljs`,
 * opened from `session_item.cljs:81-83`).
 *
 * v1's dialog is 916px wide with `maxHeight: calc(100vh - 56px)`
 * (`components/modal.cljs:38-67`). Almost nothing differs between this modal and
 * the /sessions/:id dedicated page — `is-dedicated-page?` only toggles a bottom
 * padding and the close button — so this component is the whole surface.
 *
 * Being built incrementally; the remaining blocks (review actions, script,
 * output, playback, credentials) land in follow-up commits on this branch.
 */
export default function SessionDetailsModal() {
  const detail = useSessionsStore((s) => s.detail)
  const closeDetail = useSessionsStore((s) => s.closeDetail)

  const opened = Boolean(detail.session)
  const session = detail.status === 'ready' ? detail.session : null

  return (
    <Modal opened={opened} onClose={closeDetail} title="Session Details" size={916}>
      {detail.status === 'loading' && <PageLoader h={240} />}
      {detail.status === 'error' && (
        <PageLoader h={240} error={detail.error ?? 'Failed to load the session.'} />
      )}
      {session && (
        <Stack gap="xl">
          <SessionInfo session={session} />
          <ReviewActions session={session} />
        </Stack>
      )}
    </Modal>
  )
}
