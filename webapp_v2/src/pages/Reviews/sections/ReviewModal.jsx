import { useEffect } from 'react'
import { Box, Group, Stack, Text } from '@mantine/core'
import {
  BadgeCheck,
  CalendarArrowUp,
  CircleCheckBig,
  CircleUser,
  Container,
  Hash,
  OctagonX,
  Package,
  Users,
} from 'lucide-react'
import Alert from '@/components/Alert'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import CodeSnippet from '@/components/CodeSnippet'
import Modal from '@/components/Modal'
import Tooltip from '@/components/Tooltip'
import { formatFullDate, formatRelativeTime } from '@/utils/datetime'
import { useReviewStore } from '../store'
import {
  STATUS,
  canApprove,
  canReject,
  decidedGroups,
  isSettled,
  reviewSource,
  statusLabel,
} from '../helpers'

const LABEL_WIDTH = 192

const GROUP_VERDICT = {
  [STATUS.APPROVED]: { Icon: CircleCheckBig, color: 'green', label: 'Approved', verb: 'approved' },
  [STATUS.REJECTED]: { Icon: OctagonX, color: 'red', label: 'Rejected', verb: 'rejected' },
  [STATUS.REVOKED]: { Icon: OctagonX, color: 'red', label: 'Revoked', verb: 'revoked' },
}

function DetailRow({ icon, label, children }) {
  const Icon = icon
  return (
    <Group gap="md" align="center" wrap="nowrap">
      <Group gap="sm" w={LABEL_WIDTH} wrap="nowrap" c="dimmed">
        <Icon size={20} />
        <Text size="sm">{label}</Text>
      </Group>
      <Box miw={0}>{children}</Box>
    </Group>
  )
}

function GroupRow({ group }) {
  const verdict = GROUP_VERDICT[group.status] ?? GROUP_VERDICT[STATUS.APPROVED]
  const Icon = verdict.Icon
  const reviewer = group.reviewed_by?.email || group.reviewed_by?.name

  return (
    <Group gap="lg" align="center" wrap="nowrap">
      <Group gap="sm" w={LABEL_WIDTH - 32} wrap="nowrap" c="dimmed">
        <Users size={20} />
        <Text size="sm" truncate>
          {group.group}
        </Text>
      </Group>
      <Badge variant="light" color={verdict.color} fullLabel>
        <Group gap={4} wrap="nowrap">
          <Icon size={13} />
          {verdict.label}
        </Group>
      </Badge>
      {reviewer && (
        <Group gap="xs" wrap="nowrap" c="dimmed">
          <CircleUser size={16} />
          <Text size="sm">
            <Text span fw={500} c="var(--mantine-color-text)">
              {reviewer}
            </Text>
            {` ${verdict.verb} this${group.review_date ? ` ${formatRelativeTime(group.review_date)}` : ''}`}
          </Text>
        </Group>
      )}
    </Group>
  )
}

function Statement({ sessionId }) {
  const statement = useReviewStore((s) => s.statements[sessionId])
  const status = useReviewStore((s) => s.statementStatus[sessionId])
  const fetchStatement = useReviewStore((s) => s.fetchStatement)

  useEffect(() => {
    if (sessionId) fetchStatement(sessionId)
  }, [sessionId, fetchStatement])

  if (status === 'error') {
    return (
      <Alert color="red" variant="light" radius="md">
        Failed to load the statement. The decision below still applies.
      </Alert>
    )
  }
  if (status !== 'success') {
    return (
      <Text size="sm" c="dimmed">
        Loading the statement...
      </Text>
    )
  }
  if (!statement) {
    return (
      <Text size="sm" c="dimmed">
        This review carries no statement.
      </Text>
    )
  }
  return <CodeSnippet code={statement} />
}

export default function ReviewModal({
  review,
  sidecarsById,
  user,
  opened,
  onClose,
  onApprove,
  onReject,
  submitting,
}) {
  if (!review) return null

  const source = reviewSource(review, sidecarsById)
  const status = statusLabel(review.status)
  const decided = decidedGroups(review)
  const settled = isSettled(review)
  const mayApprove = canApprove(review, user)
  const mayReject = canReject(review, user)

  return (
    <Modal opened={opened} onClose={onClose} title="Review Details" size="xl">
      <Stack gap="xl">
        <Stack gap="md">
          <DetailRow icon={Package} label="Listener">
            <Badge variant="light" color="gray" fullLabel>
              {source.primary}
            </Badge>
          </DetailRow>

          {source.secondary && (
            <DetailRow icon={Container} label="Sidecar">
              <Badge variant="light" color="gray" fullLabel>
                {source.secondary}
              </Badge>
            </DetailRow>
          )}

          {review.access_request_rule_name && (
            <DetailRow icon={BadgeCheck} label="Approval Rule">
              <Text size="sm" fw={500}>
                {review.access_request_rule_name}
              </Text>
            </DetailRow>
          )}

          <DetailRow icon={CircleCheckBig} label="Review">
            <Badge variant="light" color={status.color} fullLabel>
              {status.label}
            </Badge>
          </DetailRow>

          {decided.length > 0 && (
            <Stack gap="md" ml={32}>
              {decided.map((group) => (
                <GroupRow key={group.id} group={group} />
              ))}
            </Stack>
          )}

          <DetailRow icon={CalendarArrowUp} label="Created at">
            <Text size="sm" fw={500}>
              {formatFullDate(review.created_at)}
            </Text>
          </DetailRow>

          <DetailRow icon={Hash} label="ID">
            <Text size="sm" fw={500}>
              {review.id}
            </Text>
          </DetailRow>
        </Stack>

        {review.rejection_reason && (
          <Alert color="red" variant="light" radius="md" title="Rejection reason">
            {review.rejection_reason}
          </Alert>
        )}

        <Stack gap="sm">
          <Text size="sm" fw={600}>
            Statement
          </Text>
          <Statement sessionId={review.session} />
        </Stack>

        {/* Rejecting an approved review still stops it: a retry only forwards
            while the status is APPROVED, so this is the window to undo one. */}
        {!settled && (
          <Group justify="flex-end" gap="sm">
            <Button
              variant="subtle"
              color="red"
              onClick={onReject}
              disabled={!mayReject || submitting}
            >
              Reject
            </Button>
            {review.status === STATUS.PENDING && (
              <Tooltip
                label={`Only ${(review.review_groups_data ?? []).map((g) => g.group).join(' or ')} can approve this review`}
                disabled={mayApprove}
              >
                <span>
                  <Button onClick={onApprove} disabled={!mayApprove} loading={submitting}>
                    Approve
                  </Button>
                </span>
              </Tooltip>
            )}
          </Group>
        )}
      </Stack>
    </Modal>
  )
}
