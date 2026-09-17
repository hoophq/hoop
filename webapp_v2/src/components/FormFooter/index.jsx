import { Group } from '@mantine/core'
import classes from './FormFooter.module.css'

// Space the page must leave at the end of its content so the last controls are
// not covered by the bar. A raw px number, like PAGE_PADDING in layout/.
export const FORM_FOOTER_CLEARANCE = 120

/**
 * Action bar pinned to the bottom of the viewport, for a form long enough that
 * its Save would otherwise scroll out of reach.
 *
 * `left` is the secondary action (a Back button); `children` are the primary
 * ones, right-aligned. The component owns only the chrome, so each form keeps
 * its own buttons and their wording.
 *
 * The caller pads its own content by FORM_FOOTER_CLEARANCE. The bar is fixed,
 * so it takes no space in the flow and would otherwise sit on top of whatever
 * ends the page.
 *
 * # Why fixed, and why that CSS variable
 *
 * The body is the scroll container — nothing sets overflow on main, body or
 * #root — so `sticky; bottom: 0` only pins while the parent extends past the
 * fold. A short form would leave the bar sitting mid-page.
 *
 * The left edge follows `--app-shell-navbar-offset`, which Mantine publishes on
 * the AppShell root: the navbar's width, and 0 below its breakpoint. Mantine's
 * own AppShell.Header does exactly this under layout="alt". Reading the
 * variable rather than recomputing the width from a store keeps one definition
 * of where the navbar ends, and moves with it when it collapses.
 */
export default function FormFooter({ left, children }) {
  return (
    <Group justify={left ? 'space-between' : 'flex-end'} align="center" className={classes.root}>
      {left}
      <Group gap="md">{children}</Group>
    </Group>
  )
}
