import { Badge, rem } from '@mantine/core'

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

export const BadgeTheme = Badge.extend({
  vars: (_theme, props) => ({ root: props.size === 'chip' ? CHIP : {} }),
})
