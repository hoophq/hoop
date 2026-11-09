import { useState } from 'react'
import { Box, Collapse, Group, Image, Stack, Text } from '@mantine/core'
import {
  ArrowUpRight,
  BadgeCheck,
  Box as BoxIcon,
  CalendarArrowDown,
  CalendarArrowUp,
  ChevronDown,
  ChevronUp,
  CircleCheck,
  CircleUser,
  CirclePlay,
  ExternalLink,
  FastForward,
  Hash,
  KeyRound,
  Rotate3d,
  Sparkles,
} from 'lucide-react'
import Avatar from '@/components/Avatar'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { formatFullDate } from '@/utils/datetime'
import InfoRow from '../components/InfoRow'
import ReviewBadge from '../components/ReviewBadge'
import SessionStatusBadge from '../components/SessionStatusBadge'
import { executedBy } from '../sessionState'
import classes from './InfoModule.module.css'

// The module always shows its first three details; "See more" shows the rest.
const ALWAYS_VISIBLE = 3

const AI_ACTIONS = {
  block_execution: { color: 'red', label: 'ACTION BLOCKED' },
  allow_execution: { color: 'green', label: 'ACTION ALLOWED' },
}

// Initials from the first and the last word of the name.
const initialsOf = (name = '') => {
  const words = name.trim().split(/\s+/).filter(Boolean)
  if (!words.length) return '?'
  return `${words[0][0]}${words.length > 1 ? words[words.length - 1][0] : ''}`.toUpperCase()
}

const openInNewTab = (url) => window.open(url, '_blank', 'noopener')

function LinkButton({ children, onClick }) {
  return (
    <Button size="compact-xs" variant="light" rightSection={<ArrowUpRight size={14} />} onClick={onClick}>
      {children}
    </Button>
  )
}

function NameBadge({ icon, children }) {
  return (
    <Badge color="gray" variant="light" tag chip icon={icon && <Image src={icon} alt="" w={12} h={12} />}>
      {children}
    </Badge>
  )
}

// A listener session carries its sidecar in the metadata. The session a sidecar
// review waits in is filed under the sidecar as its user, and the review names
// the listener.
function identityOf(session) {
  if (session.identity_type === 'sidecar') {
    const sidecar = session.metadata?.sidecar || {}
    return { first: ['Listener', sidecar.listener], second: ['Sidecar', sidecar.name], icon: false }
  }
  if (session.review?.listener_name) {
    return { first: ['Listener', session.review.listener_name], second: ['Sidecar', session.user_name], icon: false }
  }
  return { first: ['Resource', session.resource_name], second: ['Role', session.role_name], icon: true }
}

export default function InfoModule({ session }) {
  const [expanded, setExpanded] = useState(false)
  const iconOf = useConnectionIconGetter()
  const identity = identityOf(session)
  const icon = identity.icon ? iconOf({ subtype: session.connection_subtype }) : null
  const jiraUrl = session.integrations_metadata?.jira_issue_url
  const aiAction = session.ai_analysis?.action
  const credentialSession = session.metadata?.credential_session
  const executor = executedBy(session)

  const rows = [
    <InfoRow key="first" icon={BoxIcon} label={identity.first[0]}>
      <NameBadge icon={icon}>{identity.first[1] || '—'}</NameBadge>
    </InfoRow>,
    <InfoRow key="second" icon={Rotate3d} label={identity.second[0]}>
      <NameBadge icon={icon}>{identity.second[1] || '—'}</NameBadge>
    </InfoRow>,
    session.review?.review_groups_data?.length > 0 && (
      <InfoRow key="review" icon={CircleCheck} label="Access Request">
        <ReviewBadge status={session.review.status} />
      </InfoRow>
    ),
    <InfoRow key="status" icon={BadgeCheck} label="Status">
      <SessionStatusBadge session={session} />
    </InfoRow>,
    <InfoRow key="user" icon={CircleUser} label="Created by">
      <Group gap="xs" wrap="nowrap">
        <Avatar size={24} radius="xl" color="blue" variant="filled">
          {initialsOf(session.user_name)}
        </Avatar>
        <Text size="sm" fw={500}>
          {session.user_name}
        </Text>
      </Group>
    </InfoRow>,
    executor && (
      <InfoRow key="executed" icon={CirclePlay} label="Executed by">
        <Text size="sm" fw={500}>
          {executor}
        </Text>
      </InfoRow>
    ),
    <InfoRow key="created" icon={CalendarArrowUp} label="Created at">
      <Text size="sm" fw={500}>
        {formatFullDate(session.start_date)}
      </Text>
    </InfoRow>,
    session.end_date && (
      <InfoRow key="finished" icon={CalendarArrowDown} label="Finished at">
        <Text size="sm" fw={500}>
          {formatFullDate(session.end_date)}
        </Text>
      </InfoRow>
    ),
    <InfoRow key="id" icon={Hash} label="ID">
      <Text size="sm" fw={500}>
        {session.id}
      </Text>
    </InfoRow>,
    jiraUrl && (
      <InfoRow key="jira" icon={ExternalLink} label="Integrations">
        <LinkButton onClick={() => openInNewTab(jiraUrl)}>Open in Jira</LinkButton>
      </InfoRow>
    ),
    session.session_batch_id && (
      <InfoRow key="batch" icon={FastForward} label="Parallel Sessions">
        <LinkButton
          onClick={() =>
            openInNewTab(`/sessions/filtered?batch_id=${encodeURIComponent(session.session_batch_id)}`)
          }
        >
          Open Parallel Summary
        </LinkButton>
      </InfoRow>
    ),
    aiAction && (
      <InfoRow key="ai" icon={Sparkles} label="AI Session Analyzer">
        <Badge color={AI_ACTIONS[aiAction]?.color || 'gray'} variant="light" chip>
          {AI_ACTIONS[aiAction]?.label || aiAction}
        </Badge>
      </InfoRow>
    ),
    credentialSession && (
      <InfoRow key="credentials" icon={KeyRound} label="Credentials Session">
        <LinkButton onClick={() => openInNewTab(`/sessions/${encodeURIComponent(credentialSession)}`)}>
          Open Session
        </LinkButton>
      </InfoRow>
    ),
  ].filter(Boolean)

  const visible = rows.slice(0, ALWAYS_VISIBLE)
  const more = rows.slice(ALWAYS_VISIBLE)

  return (
    <Stack gap="md">
      <Stack gap="md">{visible}</Stack>
      {more.length > 0 && (
        <>
          <Collapse in={expanded}>
            <Stack gap="md">{more}</Stack>
          </Collapse>
          <Box className={expanded ? undefined : classes.fade}>
            <Button
              size="compact-xs"
              variant="subtle"
              rightSection={expanded ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
              onClick={() => setExpanded((v) => !v)}
              aria-expanded={expanded}
            >
              {expanded ? 'See less' : 'See more'}
            </Button>
          </Box>
        </>
      )}
    </Stack>
  )
}
