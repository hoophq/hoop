import classes from './Layout.module.css'

/**
 * "Skip to main content" — must be the first focusable element on the page.
 *
 * It used to live inside Sidebar and was handed to both the collapsed and the
 * expanded layer. Both layers are always in the DOM (they are hidden with
 * opacity/pointer-events, and aria-hidden does not remove an element from the
 * tab sequence), so there were two reachable skip links. Now there is one, and
 * Layout renders it before AppShell so the header controls cannot precede it.
 */
export function SkipLink() {
  return (
    <a
      href="#main-content"
      className={classes.skipLink}
      onClick={(e) => {
        e.preventDefault()
        document.getElementById('main-content')?.focus()
      }}
    >
      Skip to main content
    </a>
  )
}
