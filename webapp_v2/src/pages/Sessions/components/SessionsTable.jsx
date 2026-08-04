import { Link, useNavigate } from 'react-router-dom'
import { Anchor, Group, Stack, Text } from '@mantine/core'
import { Clock } from 'lucide-react'
import Table from '@/components/Table'
import { formatFullDate } from '@/utils/datetime'
import UserAvatar from './UserAvatar'
import LiveBadge from './LiveBadge'
import ReviewStatusBadge from './ReviewStatusBadge'
import WorkflowChip from './WorkflowChip'
import { displayNameFor, isLiveSession } from '../utils'
import classes from './SessionsTable.module.css'

/**
 * The four columns are a port of `session-item` (session_item.cljs:74-125), which
 * was a headerless Radix Grid of divs. Here they are a real table with a header,
 * which is how every migrated page renders tabular data.
 *
 * Accessibility: v1 rows were clickable divs with no tabIndex, role or key
 * handler — the entire session list was unreachable without a mouse. Rather than
 * putting `role="link"` on the `<tr>` (which would strip its row semantics and
 * break table navigation), the user cell holds a real anchor. That gives
 * keyboard users a focus target, screen readers an accessible name, and
 * everybody right-click → open in a new tab, while the row keeps its
 * click-anywhere convenience.
 */
function SessionRow({ session }) {
  const navigate = useNavigate()
  const href = `/sessions/${encodeURIComponent(session.id)}`
  const name = displayNameFor(session)

  return (
    <Table.Tr className={classes.clickableRow} onClick={() => navigate(href)}>
      <Table.Td>
        <Group gap="sm" wrap="nowrap">
          <UserAvatar name={name} />
          <Anchor
            component={Link}
            to={href}
            className={classes.rowLink}
            fz="sm"
            lineClamp={1}
            onClick={(event) => event.stopPropagation()}
          >
            {name}
          </Anchor>
        </Group>
      </Table.Td>

      <Table.Td>
        <Stack gap={0}>
          <Text fz="sm" fw={600} lineClamp={1}>
            {session.connection || '—'}
          </Text>
          <Text fz="xs" c="dimmed">
            {session.type || '—'}
          </Text>
        </Stack>
      </Table.Td>

      <Table.Td>
        <Group gap="xs" wrap="wrap">
          {isLiveSession(session) && <LiveBadge />}
          <ReviewStatusBadge status={session.review?.status} />
          <WorkflowChip correlationId={session.correlation_id} />
        </Group>
      </Table.Td>

      <Table.Td>
        <Group gap={6} wrap="nowrap" c="dimmed">
          <Clock size={14} />
          <Text fz="xs">{formatFullDate(session.start_date)}</Text>
        </Group>
      </Table.Td>
    </Table.Tr>
  )
}

export default function SessionsTable({ sessions }) {
  return (
    <Table highlightOnHover>
      <Table.Thead>
        <Table.Tr>
          <Table.Th>User</Table.Th>
          <Table.Th>Resource Role</Table.Th>
          <Table.Th>Status</Table.Th>
          <Table.Th>Started</Table.Th>
        </Table.Tr>
      </Table.Thead>
      <Table.Tbody>
        {sessions.map((session) => (
          <SessionRow key={session.id} session={session} />
        ))}
      </Table.Tbody>
    </Table>
  )
}
