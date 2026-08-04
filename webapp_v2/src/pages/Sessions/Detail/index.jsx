import { Group, Stack, Title } from '@mantine/core'
import Modal from '@/components/Modal'
import PageLoader from '@/components/PageLoader'
import { useSessionsStore } from '../store'
import SessionInfo from './sections/SessionInfo'
import SessionAnalysis from './sections/SessionAnalysis'
import GuardrailsInfo from './sections/GuardrailsInfo'
import DataMaskingAnalytics from './sections/DataMaskingAnalytics'
import RejectionReason from './sections/RejectionReason'
import ReviewActions from './sections/ReviewActions'
import SessionHeaderActions from './sections/SessionHeaderActions'

/**
 * Port of the v1 session-details modal (`audit/views/session_details.cljs`,
 * opened from `session_item.cljs:81-83`).
 *
 * v1's dialog is 916px wide with `maxHeight: calc(100vh - 56px)`
 * (`components/modal.cljs:38-67`). Almost nothing differs between this modal and
 * the /sessions/:id dedicated page — `is-dedicated-page?` only toggles a bottom
 * padding and the close button — so this component is the whole surface.
 *
 * Block order follows session_details.cljs:290-355 exactly.
 * Still to come on this branch: runbook parameters, metadata rows, the script
 * area, the output/results stack, playback and the credentials block.
 */
export default function SessionDetailsModal() {
  const detail = useSessionsStore((s) => s.detail)
  const closeDetail = useSessionsStore((s) => s.closeDetail)

  const opened = Boolean(detail.session)
  const session = detail.status === 'ready' ? detail.session : null

  return (
    <Modal
      opened={opened}
      onClose={closeDetail}
      size={916}
      title={
        <Group justify="space-between" align="center" wrap="nowrap" w="100%">
          <Title order={2} size="h4">
            Session Details
          </Title>
          <SessionHeaderActions session={session} />
        </Group>
      }
    >
      {detail.status === 'loading' && <PageLoader h={240} />}
      {detail.status === 'error' && (
        <PageLoader h={240} error={detail.error ?? 'Failed to load the session.'} />
      )}
      {session && (
        <Stack gap="xl">
          <SessionInfo session={session} />
          <SessionAnalysis aiAnalysis={session.ai_analysis} />
          <GuardrailsInfo guardrailsInfo={session.guardrails_info} />
          <DataMaskingAnalytics session={session} report={detail.report} />
          <RejectionReason session={session} />
          <ReviewActions session={session} />
        </Stack>
      )}
    </Modal>
  )
}
