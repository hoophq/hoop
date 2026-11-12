import { useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { Box, Group, Stack, Text } from '@mantine/core'
import {
  BadgeCheck,
  BellRing,
  CalendarArrowUp,
  CalendarClock,
  CircleCheckBig,
  CircleUser,
  Container,
  Gauge,
  Hash,
  Hourglass,
  MessageSquareText,
  OctagonX,
  Route,
  ScrollText,
  Send,
  Timer,
  Package,
  UserX,
  Users,
  Workflow,
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
  HANDOFF_ROUTE,
  ROUTING_AI,
  STATUS,
  canApprove,
  canReject,
  canRevoke,
  decidedGroups,
  expiryLabel,
  fallbackCauseLabel,
  integrationLabel,
  isHandoffReview,
  notifyFallbackLabel,
  notifyIntegrationLabel,
  isSettled,
  isSidecarReview,
  reviewRouting,
  reviewSource,
  riskLevel,
  statusLabel,
} from '../helpers'

const LABEL_WIDTH = 192

const GROUP_VERDICT = {
  [STATUS.APPROVED]: { Icon: CircleCheckBig, color: 'green', label: 'Approved', verb: 'approved' },
  [STATUS.REJECTED]: { Icon: OctagonX, color: 'red', label: 'Rejected', verb: 'rejected' },
  [STATUS.REVOKED]: { Icon: OctagonX, color: 'red', label: 'Revoked', verb: 'revoked' },
}

// `align` "flex-start" keeps the label on the first line of a long value.
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
        This approval request carries no statement.
      </Text>
    )
  }
  return <CodeSnippet code={statement} />
}

function Requester({ sessionId }) {
  const requester = useReviewStore((s) => s.requesters[sessionId])
  if (!requester) return null
  return (
    <DetailRow icon={CircleUser} label="Created by">
      <Text size="sm" fw={500}>
        {requester}
      </Text>
    </DetailRow>
  )
}

// SidecarSession links the review to the session its statement ran in. The
// sidecar reports that session after the review settles, so it can be absent.
function SidecarSession({ sessionId, onOpen }) {
  const sidecarSession = useReviewStore((s) => s.sidecarSessions[sessionId])
  if (!sidecarSession) return null
  return (
    <DetailRow icon={ScrollText} label="Session">
      <Button variant="subtle" size="compact-sm" onClick={() => onOpen(sidecarSession)}>
        {sidecarSession}
      </Button>
    </DetailRow>
  )
}

const list = (v) => (Array.isArray(v) ? v : [])

function GrayBadges({ items }) {
  return (
    <Group gap="xs">
      {items.map((item) => (
        <Badge key={item} variant="light" color="gray" fullLabel>
          {item}
        </Badge>
      ))}
    </Group>
  )
}

// RoutingSection shows how an AI-routed review found its approvers
// (ENG-663): the AI's decision, or the static routing it fell back to.
function RoutingSection({ routing }) {
  const ai = routing.mode === ROUTING_AI
  const groups = list(routing.groups)
  const candidates = list(routing.candidates)
  const notified = list(routing.notified_users)
  const channels = list(routing.channels)
  const unreachable = list(routing.unreachable)
  const undelivered = list(routing.undelivered_channels)
  const notify = list(routing.integrations)

  return (
    <Stack gap="sm">
      <Text size="sm" fw={600}>
        Routing
      </Text>
      <Stack gap="md">
        <DetailRow icon={Route} label="Mode">
          <Badge variant="light" color={ai ? 'indigo' : 'gray'} fullLabel>
            {ai ? 'AI routing' : 'Static routing used'}
          </Badge>
        </DetailRow>

        {ai && routing.reason && (
          <DetailRow icon={MessageSquareText} label="Reason" align="flex-start">
            <Text size="sm">{routing.reason}</Text>
          </DetailRow>
        )}

        {!ai && (
          <DetailRow icon={MessageSquareText} label="Cause">
            <Text size="sm">{fallbackCauseLabel(routing.fallback_cause)}</Text>
          </DetailRow>
        )}

        {!ai && routing.fallback_detail && (
          <DetailRow icon={ScrollText} label="Detail" align="flex-start">
            <Text size="sm" c="dimmed">
              {routing.fallback_detail}
            </Text>
          </DetailRow>
        )}

        {candidates.length > 0 && (
          <DetailRow icon={Users} label="Candidate groups" align="flex-start">
            <GrayBadges items={candidates} />
          </DetailRow>
        )}

        {groups.length > 0 && (
          <DetailRow icon={Users} label={ai ? 'Selected groups' : 'Groups'} align="flex-start">
            <GrayBadges items={groups} />
          </DetailRow>
        )}

        {notify.length > 0 && (
          <DetailRow icon={Send} label="Notify" align="flex-start">
            <Group gap="xs">
              {notify.map((entry) => (
                <Badge key={entry.name} variant="light" color={entry.integrated ? 'gray' : 'yellow'} fullLabel>
                  {notifyIntegrationLabel(entry)}
                </Badge>
              ))}
            </Group>
          </DetailRow>
        )}

        {notified.length > 0 && (
          <DetailRow icon={BellRing} label="Notified people" align="flex-start">
            <Stack gap={6}>
              {notified.map((user) => (
                <Group key={user.id} gap="xs" wrap="nowrap">
                  <Text size="sm" fw={500}>
                    {user.name || user.email || user.id}
                  </Text>
                  {list(user.integrations).map((key) => (
                    <Badge key={key} variant="light" color="gray" fullLabel>
                      {integrationLabel(key)}
                    </Badge>
                  ))}
                </Group>
              ))}
            </Stack>
          </DetailRow>
        )}

        {channels.length > 0 && (
          <DetailRow icon={Hash} label="Channels" align="flex-start">
            <Stack gap={6}>
              {channels.map((channel) => (
                <Text key={`${channel.integration}:${channel.id}`} size="sm">
                  <Text span size="sm" c="dimmed">
                    {`${integrationLabel(channel.integration)} · `}
                  </Text>
                  {channel.name || channel.id}
                </Text>
              ))}
            </Stack>
          </DetailRow>
        )}

        {unreachable.length > 0 && (
          <DetailRow icon={UserX} label="Not reached" align="flex-start">
            <Stack gap={6}>
              {unreachable.map((entry) => (
                <Text key={`${entry.user_id}:${entry.integration}`} size="sm">
                  {`${entry.name || entry.user_id} (${integrationLabel(entry.integration)})`}
                  <Text span size="sm" c="dimmed">
                    {entry.cause ? `: ${entry.cause}` : ''}
                  </Text>
                </Text>
              ))}
            </Stack>
          </DetailRow>
        )}

        {undelivered.length > 0 && (
          <DetailRow icon={Hash} label="Channels not reached" align="flex-start">
            <Stack gap={6}>
              {undelivered.map((channel) => (
                <Text key={`${channel.integration}:${channel.id}`} size="sm">
                  {`${channel.id} (${integrationLabel(channel.integration)})`}
                  <Text span size="sm" c="dimmed">
                    {channel.cause ? `: ${channel.cause}` : ''}
                  </Text>
                </Text>
              ))}
            </Stack>
          </DetailRow>
        )}

        {routing.notify_fallback && (
          <DetailRow icon={BellRing} label="Notification fallback" align="flex-start">
            <Text size="sm">
              {notifyFallbackLabel(
                routing.notify_fallback,
                routing.notify_fallback_posted,
                routing.notify_fallback_delivery,
              )}
            </Text>
          </DetailRow>
        )}

        {routing.decided_at && (
          <DetailRow icon={CalendarClock} label="Decided at">
            <Text size="sm" fw={500}>
              {formatFullDate(routing.decided_at)}
            </Text>
          </DetailRow>
        )}
      </Stack>
    </Stack>
  )
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
  const navigate = useNavigate()
  if (!review) return null

  const source = reviewSource(review, sidecarsById)
  const status = statusLabel(review)
  const decided = decidedGroups(review)
  const settled = isSettled(review)
  const mayApprove = canApprove(review, user)
  const mayReject = canReject(review, user)
  const revocable = canRevoke(review)
  const expiry = expiryLabel(review)
  const risk = riskLevel(review)
  const routing = reviewRouting(review)

  return (
    <Modal opened={opened} onClose={onClose} title="Approval Details" size="xl">
      <Stack gap="xl">
        <Stack gap="md">
          <DetailRow icon={Package} label="Resource">
            <Badge variant="light" color="gray" fullLabel>
              {source.resource}
            </Badge>
          </DetailRow>

          {!isSidecarReview(review) && <Requester sessionId={review.session} />}

          {source.sidecar && (
            <DetailRow icon={Container} label="Sidecar">
              <Badge variant="light" color="gray" fullLabel>
                {source.sidecar}
              </Badge>
            </DetailRow>
          )}

          {source.listener && (
            <DetailRow icon={Container} label="Listener">
              <Badge variant="light" color="gray" fullLabel>
                {source.listener}
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

          {risk && (
            <DetailRow icon={Gauge} label="Risk level">
              <Badge variant="light" color={risk.color} fullLabel>
                {risk.label}
              </Badge>
            </DetailRow>
          )}

          {isHandoffReview(review) && (
            <DetailRow icon={Workflow} label="Route">
              <Text size="sm" fw={500}>
                {HANDOFF_ROUTE}
              </Text>
            </DetailRow>
          )}

          <DetailRow icon={CircleCheckBig} label="Approval">
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

          <SidecarSession
            sessionId={review.session}
            onOpen={(id) => navigate(`/sessions/${encodeURIComponent(id)}`)}
          />
        </Stack>

        {review.rejection_reason && (
          <Alert color="red" variant="light" radius="md" title="Rejection reason">
            {review.rejection_reason}
          </Alert>
        )}

        {review.status === STATUS.EXPIRED && (
          <Alert color="gray" variant="light" radius="md" title="Approval request expired">
            Nothing was released. Running the statement again files a new approval request.
          </Alert>
        )}

        {routing && <RoutingSection routing={routing} />}

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
                label={`Only ${(review.review_groups_data ?? []).map((g) => g.group).join(' or ')} can approve this request`}
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
