import { Badge as MantineBadge } from '@mantine/core'
import classes from './Badge.module.css'

/**
 * Semantic status badge. Use `variant` to express meaning:
 *   - "active" → green filled
 *   - "inactive" → gray outline
 *   - "warning" → amber light
 *   - "danger" → red filled
 * Falls back to standard Mantine props when `variant` is a Mantine variant name.
 * `fullLabel` keeps a short label from being ellipsized (see Badge.module.css).
 */
const SEMANTIC_MAP = {
  active: { color: 'green', variant: 'filled' },
  inactive: { color: 'gray', variant: 'outline' },
  // Amber, and light rather than filled. `yellow` is not a palette this theme
  // defines, so it fell through to stock Mantine #fcc419 and painted white on
  // it: 1.57:1, which is not readable at any size. Amber's own light variant
  // is barely better (2.05:1) because its light-color is the primary shade, so
  // the text is pinned two steps darker — 5.92:1, AA at 12px.
  warning: { color: 'amber', variant: 'light', c: 'amber.9' },
  danger: { color: 'red', variant: 'filled' },
}

// A badge carrying a phrase rather than a status. Mantine upper-cases and bolds
// every badge, which reads as a state machine ("ACTIVE", "WAITING") and turns a
// written label into shouting. `tag` is the opt-out, and it lives here so no
// call site has to repeat the two props.
const TAG_PROPS = { tt: 'none', fw: 500 }

/**
 * `icon` is a leading icon, and it is a prop rather than a bare `leftSection`
 * because the two cases need different geometry:
 *
 *   with a label  → an ordinary left section, spaced away from the text
 *   without one   → a square chip. Mantine still lays out the section against
 *                   the label that is not there, so the icon lands off centre
 *                   with dead space to its right. Badge.module.css squares the
 *                   box and re-centres it.
 *
 * `chip` picks the taller content-chip size from theme.js. Use it for anything
 * carrying an icon or a phrase; leave it off for a status word.
 */
export default function Badge({
  variant = 'filled',
  size = 'sm',
  color,
  tag,
  chip,
  icon,
  fullLabel = false,
  classNames = {},
  children,
  ...props
}) {
  const semantic = SEMANTIC_MAP[variant]
  const resolvedColor = semantic?.color ?? color
  const resolvedVariant = semantic?.variant ?? variant
  const iconOnly = Boolean(icon) && children == null
  // Least specific first, so a call site's own classNames still win over both
  // of the opt-ins this component owns.
  const merged = {
    ...(iconOnly ? { root: classes.iconOnly, section: classes.iconOnlySection } : null),
    ...(fullLabel ? { label: classes.fullLabel } : null),
    ...classNames,
  }

  return (
    <MantineBadge
      variant={resolvedVariant}
      color={resolvedColor}
      c={semantic?.c}
      size={chip ? 'chip' : size}
      radius="sm"
      leftSection={icon}
      classNames={merged}
      {...(tag ? TAG_PROPS : null)}
      {...props}
    >
      {children}
    </MantineBadge>
  )
}
