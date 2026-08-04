import { useState } from 'react'
import { Anchor, Avatar, Box, Button, Collapse, Group, Stack, Text } from '@mantine/core'
import {
  ArrowUpRight,
  BadgeCheck,
  BookUp2,
  CalendarArrowDown,
  CalendarArrowUp,
  ChevronDown,
  ChevronUp,
  CircleUser,
  ExternalLink,
  FastForward,
  Hash,
  KeyRound,
  Package,
  Rotate3d,
  Sparkles,
  Users,
} from 'lucide-react'
import Badge from '@/components/Badge'
import Tooltip from '@/components/Tooltip'
import { formatFullDate } from '@/utils/datetime'
import ReviewStatusBadge from '../../components/ReviewStatusBadge'
import { initialsFor } from '../../utils'

/**
 * Port of `sessions/components/session_details.cljs` (259 LOC) — the detail rows
 * at the top of the session modal.
 *
 * v1 renders these as a fixed two-column layout: a `w-48` label column with a
 * 20px icon, and a value column. The "See more" state is component-local and
 * resets on every mount, same as v1.
 */

const STATUS_BADGE = {
  done: { color: 'green', label: 'Success' },
  ready: { color: 'blue', label: 'Ready' },
  error: { color: 'red', label: 'Error' },
}

/** v1's `review-action-verb` — PENDING groups deliberately show no reviewer line. */
const REVIEW_VERB = { APPROVED: 'approved', REJECTED: 'rejected' }

function DetailRow({ icon, label, children }) {
  const Icon = icon
  return (
    <Group align="flex-start" wrap="nowrap" gap="md">
      {/* v1 pins the label column at w-48 so every value lines up. */}
      <Group gap="xs" wrap="nowrap" w={192}>
        <Icon size={20} />
        <Text size="sm" c="dimmed">
          {label}
        </Text>
      </Group>
      <Box>{children}</Box>
    </Group>
  )
}

function ReviewGroupItem({ group }) {
  const reviewer = group.reviewed_by
  const name = reviewer?.email || reviewer?.name
  const verb = REVIEW_VERB[group.status]

  return (
    <Group gap="lg" wrap="nowrap" align="center">
      <Group gap="xs" wrap="nowrap" w={128}>
        <Users size={20} />
        <Text size="sm" lineClamp={1}>
          {group.group}
        </Text>
      </Group>
      <ReviewStatusBadge status={group.status} />
      {name && verb && (
        <Group gap="xs" wrap="nowrap">
          <CircleUser size={16} />
          <Text size="sm" fw={500}>
            {name}
          </Text>
          <Text size="sm" c="dimmed">
            {`${verb} this`}
          </Text>
          {group.review_date && (
            <Tooltip label={formatFullDate(group.review_date)}>
              <Text size="sm" c="dimmed">
                {formatFullDate(group.review_date)}
              </Text>
            </Tooltip>
          )}
        </Group>
      )}
    </Group>
  )
}

export default function SessionInfo({ session }) {
  const [expanded, setExpanded] = useState(false)
  if (!session) return null

  const review = session.review
  const reviewGroups = review?.review_groups_data ?? []
  const status = STATUS_BADGE[session.status] ?? {
    color: 'gray',
    label: session.status || 'Unknown',
  }
  const jiraUrl = session.integrations_metadata?.jira_issue_url
  const runbookFile = session.labels?.runbookFile
  const aiAction = session.ai_analysis?.action
  const credentialSession = session.metadata?.credential_session

  const openExternal = (url) => window.open(url, '_blank')

  return (
    <Stack gap="md">
      <DetailRow icon={Package} label="Resource">
        <Badge color="gray" variant="light" size="lg">
          {session.resource_name || '—'}
        </Badge>
      </DetailRow>

      <DetailRow icon={Rotate3d} label="Role">
        <Badge color="gray" variant="light" size="lg">
          {session.role_name || session.connection || '—'}
        </Badge>
      </DetailRow>

      {review && (
        <>
          <DetailRow icon={BadgeCheck} label="Access Request">
            <ReviewStatusBadge status={review.status} />
          </DetailRow>
          {reviewGroups.length > 0 && (
            <Stack gap="md" ml={32}>
              {reviewGroups.map((group) => (
                <ReviewGroupItem key={group.id} group={group} />
              ))}
            </Stack>
          )}
        </>
      )}

      <DetailRow icon={BadgeCheck} label="Status">
        <Badge color={status.color} variant="light">
          {status.label}
        </Badge>
      </DetailRow>

      <Collapse in={expanded}>
        <Stack gap="md">
          <DetailRow icon={CircleUser} label="Created by">
            <Group gap="xs" wrap="nowrap">
              {/* v1 threw here when user_name was nil; initialsFor guards it. */}
              <Avatar size="sm" radius="xl" color="dark" variant="filled">
                {initialsFor(session.user_name)}
              </Avatar>
              <Text size="sm">{session.user_name || session.user || '—'}</Text>
            </Group>
          </DetailRow>

          <DetailRow icon={CalendarArrowUp} label="Created at">
            <Text size="sm">{formatFullDate(session.start_date)}</Text>
          </DetailRow>

          {session.end_date && (
            <DetailRow icon={CalendarArrowDown} label="Finished at">
              <Text size="sm">{formatFullDate(session.end_date)}</Text>
            </DetailRow>
          )}

          <DetailRow icon={Hash} label="ID">
            <Text size="sm">{session.id}</Text>
          </DetailRow>

          {runbookFile && (
            <DetailRow icon={BookUp2} label="Runbook">
              <Text size="sm">{runbookFile}</Text>
            </DetailRow>
          )}

          {jiraUrl && (
            <DetailRow icon={ExternalLink} label="Integrations">
              <Button
                size="compact-sm"
                variant="light"
                color="gray"
                rightSection={<ArrowUpRight size={16} />}
                onClick={() => openExternal(jiraUrl)}
              >
                Open in Jira
              </Button>
            </DetailRow>
          )}

          {session.session_batch_id && (
            <DetailRow icon={FastForward} label="Parallel Sessions">
              <Button
                size="compact-sm"
                variant="light"
                color="gray"
                rightSection={<ArrowUpRight size={16} />}
                onClick={() =>
                  openExternal(
                    `${window.location.origin}/sessions/filtered?${new URLSearchParams({
                      batch_id: session.session_batch_id,
                    })}`
                  )
                }
              >
                Open Parallel Summary
              </Button>
            </DetailRow>
          )}

          {aiAction && (
            <DetailRow icon={Sparkles} label="AI Session Analyzer">
              {aiAction === 'block_execution' ? (
                <Badge color="red" variant="light">
                  ACTION BLOCKED
                </Badge>
              ) : aiAction === 'allow_execution' ? (
                <Badge color="green" variant="light">
                  ACTION ALLOWED
                </Badge>
              ) : (
                <Badge color="gray" variant="light">
                  {aiAction}
                </Badge>
              )}
            </DetailRow>
          )}

          {credentialSession && (
            <DetailRow icon={KeyRound} label="Credentials Session">
              <Button
                size="compact-sm"
                variant="light"
                color="gray"
                rightSection={<ArrowUpRight size={14} />}
                onClick={() =>
                  openExternal(
                    `${window.location.origin}/sessions/${encodeURIComponent(credentialSession)}`
                  )
                }
              >
                Open Session
              </Button>
            </DetailRow>
          )}
        </Stack>
      </Collapse>

      <Box>
        <Anchor component="button" type="button" onClick={() => setExpanded((v) => !v)}>
          <Group gap={4} wrap="nowrap">
            <Text size="sm" fw={500}>
              {expanded ? 'See less' : 'See more'}
            </Text>
            {expanded ? <ChevronUp size={16} /> : <ChevronDown size={16} />}
          </Group>
        </Anchor>
      </Box>
    </Stack>
  )
}
