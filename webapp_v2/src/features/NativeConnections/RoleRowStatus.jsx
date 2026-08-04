import { Group, Text, ThemeIcon, VisuallyHidden } from '@mantine/core'
import { Clock, Radio, WifiOff } from 'lucide-react'
import Badge from '@/components/Badge'
import { SessionTimer } from './components/SessionTimer'
import { ROW_STATE } from './rowState'

/**
 * The right-hand slot of a collapsed row.
 *
 * Everything here is presentational — no buttons. The whole row is a single
 * Accordion.Control (a <button>), and nesting an interactive element inside it
 * is invalid HTML that breaks keyboard navigation and the accessible name.
 * "Ask access" therefore reads as a pill; the real submit lives in the panel.
 */
export function RoleRowStatus({ state, active }) {
  switch (state) {
    case ROW_STATE.ACTIVE_BOUNDED:
      return (
        <Group gap={4} wrap="nowrap">
          <Badge variant="inactive" radius="xl" leftSection={<Clock size={12} aria-hidden="true" />}>
            <SessionTimer expireAt={active.expire_at} fz="xs" />
          </Badge>
        </Group>
      )

    case ROW_STATE.ACTIVE_PERSISTENT:
      return (
        <ThemeIcon variant="light" color="green" size="sm" radius="xl">
          <Radio size={12} aria-hidden="true" />
          <VisuallyHidden>Connected</VisuallyHidden>
        </ThemeIcon>
      )

    case ROW_STATE.NEEDS_REVIEW:
      return <Badge variant="light" color="indigo" radius="xl">Ask access</Badge>

    case ROW_STATE.PENDING_REVIEW:
      return <Badge variant="light" color="blue" radius="xl">Pending review</Badge>

    case ROW_STATE.OFFLINE:
      return (
        <Badge variant="inactive" radius="xl" leftSection={<WifiOff size={12} aria-hidden="true" />}>
          Offline
        </Badge>
      )

    case ROW_STATE.ACCESS_REVOKED:
      return <Badge variant="danger" radius="xl">Access revoked</Badge>

    default:
      return <Text component="span" />
  }
}
