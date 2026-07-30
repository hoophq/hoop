import { Button, rem } from '@mantine/core'

// App-wide button height scale: xs=24px, sm=32px, md=40px (default, no
// size prop needed at call sites), lg=48px. Heights come from the global
// --hoop-control-height-* variables (src/theme.js cssVariablesResolver) so
// Button, ActionIcon, and inputs stay on one scale. Padding and font size
// are re-derived per height (24↔12px text, 32↔14, 40↔16, 48↔18) —
// Mantine's stock presets assume a different height ramp (sm=36, md=42,
// lg=50).
//
// compact-* sizes are intentionally left on Mantine presets: they are a
// separate axis (minimal height/padding for inline text-like buttons), not
// part of the 4-step scale.
const SIZES = {
  xs: { paddingX: rem(8), fz: 'var(--mantine-font-size-xs)' },
  sm: { paddingX: rem(12), fz: 'var(--mantine-font-size-sm)' },
  md: { paddingX: rem(16), fz: 'var(--mantine-font-size-md)' },
  lg: { paddingX: rem(20), fz: 'var(--mantine-font-size-lg)' },
}

export const ButtonTheme = Button.extend({
  defaultProps: { size: 'md' },
  vars: (_theme, props) => {
    const s = SIZES[props.size]
    if (!s) return { root: {} }
    return {
      root: {
        '--button-height': `var(--hoop-control-height-${props.size})`,
        '--button-padding-x': s.paddingX,
        '--button-fz': s.fz,
      },
    }
  },
})
