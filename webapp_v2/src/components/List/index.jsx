import { Box } from '@mantine/core'
import classes from './List.module.css'

/**
 * A bordered block of rows: the one list shape of the app. It owns the
 * outer border, the corners (`--hoop-list-radius`) and the line between
 * rows, so a row draws none of them and needs no first/last flag.
 *
 * Each direct child is a row. Pass `component="ul"` for a semantic list
 * whose rows are `li`.
 */
export default function List({ component = 'div', className, children, ...props }) {
  return (
    <Box
      component={component}
      className={className ? `${classes.root} ${className}` : classes.root}
      {...props}
    >
      {children}
    </Box>
  )
}
