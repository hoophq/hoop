import { Fragment, useCallback, useEffect, useState } from 'react'
import { Card, Divider, Group, Stack, Text } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { notifications } from '@mantine/notifications'
import { useNavigate, useParams } from 'react-router-dom'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import Code from '@/components/Code'
import Modal from '@/components/Modal'
import Textarea from '@/components/Textarea'
import PageLoader from '@/components/PageLoader'
import { useMinDelay } from '@/hooks/useMinDelay'
import { sessionsService } from '@/services/sessions'
import { reviewsService } from '@/services/reviews'
import { formatDateTime, formatDurationNs } from '@/utils/dates'
import MobileHeader from '../../components/MobileHeader'
import InfoRow from '../../components/InfoRow'
import { reviewStatusVariant } from '../../statusMaps'

function ApproveModal({ opened, onClose, session, saving, onConfirm }) {
  return (
    <Modal opened={opened} onClose={onClose} title="Approve access?" size="sm">
      <Stack>
        <Text size="sm">
          {`This approves ${session?.user_name || session?.user || 'the requester'}'s access to ${session?.connection ?? 'the connection'}.`}
        </Text>
        <Group justify="flex-end" gap="sm">
          <Button variant="subtle" color="gray" onClick={onClose}>
            Cancel
          </Button>
          <Button color="green" loading={saving} onClick={onConfirm}>
            Approve
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}

function RejectModal({ opened, onClose, reason, onReasonChange, saving, onConfirm }) {
  return (
    <Modal opened={opened} onClose={onClose} title="Reject access?" size="sm">
      <Stack>
        <Textarea
          label="Reason"
          placeholder="Optional — visible to the requester"
          value={reason}
          onChange={(e) => onReasonChange(e.currentTarget.value)}
        />
        <Group justify="flex-end" gap="sm">
          <Button variant="subtle" color="gray" onClick={onClose}>
            Cancel
          </Button>
          <Button color="red" loading={saving} onClick={onConfirm}>
            Reject
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}

function ApprovalGroups({ groups }) {
  if (!groups?.length) return null

  return (
    <Card padding={0} withBorder>
      {groups.map((entry, i) => (
        <Fragment key={entry.id ?? entry.group}>
          {i > 0 && <Divider />}
          <Group justify="space-between" wrap="nowrap" px="md" py="sm">
            <Stack gap={0} miw={0}>
              <Text size="sm" fw={600} truncate="end">
                {entry.group}
              </Text>
              {entry.reviewed_by && (
                <Text size="xs" c="dimmed" truncate="end">
                  {entry.reviewed_by.name || entry.reviewed_by.email}
                </Text>
              )}
            </Stack>
            <Badge variant={reviewStatusVariant(entry.status)}>{entry.status ?? 'PENDING'}</Badge>
          </Group>
        </Fragment>
      ))}
    </Card>
  )
}

function MobileReviewDetail() {
  const { sessionId } = useParams()
  const navigate = useNavigate()
  const [session, setSession] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [saving, setSaving] = useState(false)
  const [rejectReason, setRejectReason] = useState('')
  const [approveOpened, { open: openApprove, close: closeApprove }] = useDisclosure(false)
  const [rejectOpened, { open: openReject, close: closeReject }] = useDisclosure(false)
  const showLoader = useMinDelay(loading, 500)

  function handleOpenReject() {
    setRejectReason('')
    openReject()
  }

  const fetchSession = useCallback(async () => {
    try {
      const { data } = await sessionsService.get(sessionId)
      setSession(data)
    } catch {
      setError('Failed to load review.')
    } finally {
      setLoading(false)
    }
  }, [sessionId])

  useEffect(() => {
    fetchSession()
  }, [fetchSession])

  useEffect(() => {
    if (!loading && session && !session.review) {
      notifications.show({ message: 'This session has no review.', color: 'red' })
      navigate('/m/reviews', { replace: true })
    }
  }, [loading, session, navigate])

  if (showLoader) return <PageLoader h={400} />

  const review = session?.review
  const isPending = review?.status === 'PENDING'

  async function submitDecision(status, reason) {
    setSaving(true)
    try {
      const payload = { status }
      if (status === 'REJECTED' && reason?.trim()) {
        payload.rejection_reason = reason.trim()
      }
      await reviewsService.update(review.id, payload)
      notifications.show({
        message: status === 'APPROVED' ? 'Review approved.' : 'Review rejected.',
        color: 'green',
      })
      navigate('/m/reviews')
    } catch (err) {
      const message =
        err.response?.status === 403
          ? 'Access denied.'
          : (err.response?.data?.message ?? 'Failed to update review.')
      notifications.show({ message, color: 'red' })
      closeApprove()
      closeReject()
      // The review may have changed under us (e.g. already approved elsewhere).
      await fetchSession()
    } finally {
      setSaving(false)
    }
  }

  return (
    <Stack gap="md">
      <MobileHeader title="Review" backTo="/m/reviews" />

      {error && <Text c="red">{error}</Text>}

      {session && review && (
        <>
          <Card padding={0} withBorder>
            <InfoRow label="Requested by" value={session.user_name || session.user} />
            <Divider />
            <InfoRow label="Email" value={session.user} />
            <Divider />
            <InfoRow label="Connection" value={session.connection} />
            <Divider />
            <InfoRow label="Type" value={session.verb} />
            <Divider />
            <InfoRow
              label="Access"
              value={
                review.type === 'jit'
                  ? `JIT · ${formatDurationNs(review.access_duration) ?? 'time-based'}`
                  : 'One-time'
              }
            />
            <Divider />
            <InfoRow label="Requested" value={formatDateTime(review.created_at ?? session.start_date)} />
            <Divider />
            <InfoRow label="Status">
              <Badge variant={reviewStatusVariant(review.status)}>{review.status}</Badge>
            </InfoRow>
            {review.rejection_reason && (
              <>
                <Divider />
                <InfoRow label="Rejection reason" value={review.rejection_reason} />
              </>
            )}
          </Card>

          <ApprovalGroups groups={review.review_groups_data} />

          {session.script?.data && (
            <Stack gap="xs">
              <Text size="sm" fw={600}>
                Input
              </Text>
              <Code block mah={260} w="100%">
                {session.script.data}
              </Code>
            </Stack>
          )}

          {isPending && (
            <Group grow>
              <Button variant="light" color="red" onClick={handleOpenReject}>
                Reject
              </Button>
              <Button color="green" onClick={openApprove}>
                Approve
              </Button>
            </Group>
          )}

          <ApproveModal
            opened={approveOpened}
            onClose={closeApprove}
            session={session}
            saving={saving}
            onConfirm={() => submitDecision('APPROVED')}
          />
          <RejectModal
            opened={rejectOpened}
            onClose={closeReject}
            reason={rejectReason}
            onReasonChange={setRejectReason}
            saving={saving}
            onConfirm={() => submitDecision('REJECTED', rejectReason)}
          />
        </>
      )}
    </Stack>
  )
}

export default MobileReviewDetail
