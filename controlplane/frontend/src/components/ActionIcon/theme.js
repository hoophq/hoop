import { ActionIcon } from '@mantine/core'

// ActionIcon shares the app-wide control height scale (xs=24px, sm=32px,
// md=40px default, lg=48px) via the global --hoop-control-height-*
// variables from src/theme.js, so icon buttons line up with adjacent
// buttons and inputs. Sizes outside the scale (numeric, input-*) keep
// Mantine presets.
const SIZES = new Set(['xs', 'sm', 'md', 'lg'])

export const ActionIconTheme = ActionIcon.extend({
  defaultProps: { size: 'md' },
  vars: (_theme, props) => {
    if (!SIZES.has(props.size)) return { root: {} }
    return {
      root: {
        '--ai-size': `var(--hoop-control-height-${props.size})`,
      },
    }
  },
})
