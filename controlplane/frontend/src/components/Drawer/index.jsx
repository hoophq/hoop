import { Drawer as MantineDrawer } from '@mantine/core'
import classes from './Drawer.module.css'

/**
 * Side panel for the detail of a row in a list.
 *
 * Prefer this over a detail route when the user is working through a list and
 * losing their scroll position costs more than a linkable URL buys. Prefer a
 * route when the thing is worth pasting into a channel.
 *
 * Usage:
 *   <Drawer opened={Boolean(active)} onClose={clear} title={active?.name}>
 *     {children}
 *   </Drawer>
 */
export default function Drawer({ children, size = 'lg', ...props }) {
  return (
    <MantineDrawer
      position="right"
      size={size}
      padding="lg"
      classNames={{ title: classes.title, body: classes.body }}
      {...props}
    >
      {children}
    </MantineDrawer>
  )
}
