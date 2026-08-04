import { Box, Group } from '@mantine/core'
import Badge from '@/components/Badge'
import Tooltip from '@/components/Tooltip'
import classes from './LiveBadge.module.css'

// Second consumer: Wave 6 session details header (B6.1).

/** Port of `live-badge` (session_item.cljs:53-61). */
export default function LiveBadge() {
  return (
    <Tooltip label="Session is currently running">
      <Badge
        color="green"
        variant="light"
        className={classes.badge}
        aria-label="Live session"
      >
        <Group gap={4} wrap="nowrap">
          <Box className={classes.dot} />
          Live
        </Group>
      </Badge>
    </Tooltip>
  )
}
