import { Tooltip as MantineTooltip } from '@mantine/core'

/**
 * Tooltip wrapper. Adds withArrow + a small default offset.
 */
export default function Tooltip(props) {
  return <MantineTooltip withArrow offset={6} {...props} />
}
