import { Badge, rem } from '@mantine/core'
import classes from './Badge.module.css'

/**
 * `size="chip"` is the content chip, as opposed to a status badge.
 *
 * Mantine's scale stops at 18px for `sm`, which is calibrated for the 10px
 * uppercase of a status word — ACTIVE, WAITING. A chip that carries an icon, a
 * phrase or a monospace address needs the room. 22px is not a new number: it is
 * the height components/Pill/theme.js already pins for the chips inside a
 * MultiSelect, described there as one chip size everywhere. Picking anything
 * else would put two chip heights a few pixels apart in the same app.
 *
 * Mantine's own resolver reads `size` off the same props and emits
 * `var(--badge-height-chip)`, which does not exist; these run after it and win
 * (resolveVars merges the theme's vars last).
 */
const CHIP = {
  '--badge-height': rem(22),
  '--badge-fz': 'var(--mantine-font-size-xs)',
  '--badge-padding-x': 'var(--mantine-spacing-xs)',
}

/**
 * The status badge (`sm`, the default): 11px instead of Mantine's 10px, on a
 * 20px pill, so an upper-case status word stays readable.
 */
const STATUS = {
  '--badge-height': rem(20),
  '--badge-fz': rem(11),
}

/**
 * A light badge (a tinted fill) writes its text in the darkest step of its
 * color. Mantine's light-color is the primary shade, which on the light tint
 * reads at 3.5:1 for gray; step 9 clears 4.5:1 (WCAG AA) for every palette.
 * A call site that passes `c` keeps its own color.
 */
function lightText(props) {
  if (props.variant !== 'light' || props.c || typeof props.color !== 'string') return {}
  const name = props.color.split('.')[0]
  return { '--badge-color': `var(--mantine-color-${name}-9)` }
}

export const BadgeTheme = Badge.extend({
  // Every badge is a pill, from --hoop-badge-radius (Badge.module.css).
  classNames: { root: classes.root },
  vars: (_theme, props) => ({
    root: {
      ...(props.size === 'chip' ? CHIP : props.size === 'sm' || props.size == null ? STATUS : {}),
      ...lightText(props),
    },
  }),
})
