import { useEffect } from 'react'
import { Box, Group, Stack, Text } from '@mantine/core'
import {
  BadgeCheck,
  CalendarArrowUp,
  CircleCheckBig,
  CircleUser,
  Container,
  Hash,
  Hourglass,
  OctagonX,
  Timer,
  Package,
  UserRound,
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
  canRevoke,
  decidedGroups,
  expiryLabel,
  isSettled,
  isSidecarReview,
  requesterFromLabels,
  requesterMethod,
  requesterName,
  reviewSource,
  statusLabel,
} from '../helpers'

const LABEL_WIDTH = 192

const GROUP_VERDICT = {
  [STATUS.APPROVED]: { Icon: CircleCheckBig, color: 'green', label: 'Approved', verb: 'approved' },
  [STATUS.REJECTED]: { Icon: OctagonX, color: 'red', label: 'Rejected', verb: 'rejected' },
  [STATUS.REVOKED]: { Icon: OctagonX, color: 'red', label: 'Revoked', verb: 'revoked' },
}

function DetailRow({ icon, label, align = 'center', children }) {
  const Icon = icon
  return (
    <Group gap="md" align={align} wrap="nowrap">
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

// The Badge wrapper's warning variant is the readable yellow.
const METHOD_BADGE = { yellow: { variant: 'warning' } }

const SHARED_APPROVAL =
  'Approving releases this statement once, to the first caller on this listener that sends it or waits on it. That may not be the caller named here.'

// Display data only: the sidecar reports it, and the approval is shared with any caller of the same bytes.
function Filer({ sessionId, review }) {
  const labels = useReviewStore((s) => s.labels[sessionId])
  const requester = requesterFromLabels(labels)
  if (!requester) return null

  const name = requesterName(requester)
  const method = requesterMethod(requester.method)
  const badge = METHOD_BADGE[method.color] ?? { variant: 'light', color: method.color }
  const details = [
    requester.email && requester.email !== name ? requester.email : null,
    requester.peerAddr && requester.peerAddr !== name ? `from ${requester.peerAddr}` : null,
  ].filter(Boolean)
  const pending = review.status === STATUS.PENDING || review.status === STATUS.APPROVED

  return (
    <DetailRow icon={UserRound} label="Filed by" align="flex-start">
      <Stack gap={4}>
        <Group gap="sm" wrap="nowrap">
          <Text size="sm" fw={500} truncate>
            {name}
          </Text>
          <Tooltip
            label={`${method.description}. Reported by the sidecar; hoop does not verify it.`}
            multiline
            w={320}
          >
            <Badge {...badge} tag fullLabel>
              {method.label}
            </Badge>
          </Tooltip>
        </Group>
        {details.length > 0 && (
          <Tooltip label="Behind a proxy, the address is the proxy's." disabled={!requester.peerAddr}>
            <Text size="xs" c="dimmed">
              {details.join(' · ')}
            </Text>
          </Tooltip>
        )}
        {pending && (
          <Alert color="yellow" variant="light" radius="md">
            {SHARED_APPROVAL}
          </Alert>
        )}
        {review.status === STATUS.EXECUTED && (
          <Text size="xs" c="dimmed">
            {`Released once, to the first caller that claimed the approval; that may not be the filer. The sidecar audit record for review ${review.id} names who ran it.`}
          </Text>
        )}
      </Stack>
    </DetailRow>
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
  onRevoke,
  submitting,
}) {
  if (!review) return null

  const source = reviewSource(review, sidecarsById)
  const status = statusLabel(review.status)
  const decided = decidedGroups(review)
  const settled = isSettled(review)
  const mayApprove = canApprove(review, user)
  const mayReject = canReject(review, user)
  const revocable = canRevoke(review)
  const expiry = expiryLabel(review)

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

          {isSidecarReview(review) && <Filer sessionId={review.session} review={review} />}

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

          {expiry && (
            <DetailRow icon={Hourglass} label={expiry}>
              <Text size="sm" fw={500}>
                {formatFullDate(review.expires_at)}
                <Text span size="sm" c="dimmed">
                  {` (${formatRelativeTime(review.expires_at)})`}
                </Text>
              </Text>
            </DetailRow>
          )}

          {review.status === STATUS.PENDING && review.approval_ttl_sec > 0 && (
            <DetailRow icon={Timer} label="Approval lasts">
              <Text size="sm" fw={500}>
                {`${Math.max(1, Math.round(review.approval_ttl_sec / 60))} minutes`}
              </Text>
            </DetailRow>
          )}

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

        {review.status === STATUS.EXPIRED && (
          <Alert color="gray" variant="light" radius="md" title="Review expired">
            Nothing was released. Running the statement again files a new review.
          </Alert>
        )}

        <Stack gap="sm">
          <Text size="sm" fw={600}>
            Statement
          </Text>
          <Statement sessionId={review.session} />
        </Stack>

        {/* A retry only forwards while the status is APPROVED, so this is the
            window to undo an approval: revoke a sidecar review, reject others. */}
        {!settled && (
          <Group justify="flex-end" gap="sm">
            {revocable ? (
              <Button
                variant="subtle"
                color="red"
                onClick={onRevoke}
                disabled={!mayReject}
                loading={submitting}
              >
                Revoke
              </Button>
            ) : (
              <Button
                variant="subtle"
                color="red"
                onClick={onReject}
                disabled={!mayReject || submitting}
              >
                Reject
              </Button>
            )}
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
