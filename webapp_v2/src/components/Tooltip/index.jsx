import { Tooltip as MantineTooltip } from '@mantine/core'

/**
 * Tooltip wrapper. Defaults to dark background (high contrast)
 * with an arrow and a small offset.
 */
export default function Tooltip(props) {
  return <MantineTooltip color="dark" withArrow offset={6} {...props} />
}
