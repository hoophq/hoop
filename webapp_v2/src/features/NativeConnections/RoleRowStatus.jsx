import { Group, Text, ThemeIcon, VisuallyHidden } from '@mantine/core'
import { Radio, Timer, WifiOff } from 'lucide-react'
import Badge from '@/components/Badge'
import { SessionTimer } from './components/SessionTimer'
import { ROW_STATE } from './rowState'
import classes from './NativeConnections.module.css'

/** Shown whenever the connection is open, bounded or not. */
function OpenIndicator() {
  return (
    <ThemeIcon size={24} radius="xl" className={classes.activeIndicator}>
      <Radio size={12} aria-hidden="true" />
      <VisuallyHidden>Connected</VisuallyHidden>
    </ThemeIcon>
  )
}

/**
 * The right-hand slot of a collapsed row, left of the action button.
 *
 * Presentational only — this sits inside Accordion.Control, which is a <button>.
 * "Connect" and "Ask access" are real buttons and live in RoleRowAction, outside
 * the control.
 */
export function RoleRowStatus({ state, active }) {
  switch (state) {
    // The timer badge is additional to the open indicator, not instead of it:
    // it says how long is left, the indicator says the connection is open.
    case ROW_STATE.ACTIVE_BOUNDED:
      return (
        <Group gap="sm" wrap="nowrap">
          {/* Stopwatch, not a clock face — the Figma badge uses `timer`. */}
          <Badge
            variant="light"
            color="gray"
            classNames={{ root: classes.timerBadge, section: classes.timerBadgeSection }}
            leftSection={<Timer size={12} aria-hidden="true" />}
          >
            <SessionTimer expireAt={active.expire_at} fz="xs" fw={500} />
          </Badge>
          <OpenIndicator />
        </Group>
      )

    case ROW_STATE.ACTIVE_PERSISTENT:
      return <OpenIndicator />

    // Amber, matching the waiting callout in the panel. `blue` is not in the
    // theme palette at all — it was silently falling back to Mantine's stock
    // blue and sitting outside the product's colour identity.
    case ROW_STATE.PENDING_REVIEW:
      return <Badge variant="light" color="amber" radius="xl">Pending review</Badge>

    case ROW_STATE.OFFLINE:
      return (
        <Badge variant="inactive" radius="xl" leftSection={<WifiOff size={12} aria-hidden="true" />}>
          Offline
        </Badge>
      )

    case ROW_STATE.ACCESS_REVOKED:
      return <Badge variant="danger" radius="xl">Access revoked</Badge>

    // IDLE and NEEDS_REVIEW carry their affordance in the action button.
    default:
      return <Text component="span" />
  }
}
