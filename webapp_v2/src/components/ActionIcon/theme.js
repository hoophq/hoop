import { ActionIcon } from '@mantine/core'
import classes from './ActionIcon.module.css'

// ActionIcon shares the app-wide control height scale (xs=24px, sm=32px,
// md=40px default, lg=48px) via the global --hoop-control-height-*
// variables from src/theme.js, so icon buttons line up with adjacent
// buttons and inputs. Sizes outside the scale (numeric, input-*) keep
// Mantine presets.
//
// An icon button is round, like the pill Button: its corners come from
// --hoop-button-radius. Theme vars run after Mantine's own, so a `radius`
// prop on a call site does nothing.
const SIZES = new Set(['xs', 'sm', 'md', 'lg'])
const ROUND = { '--ai-radius': 'var(--hoop-button-radius)' }
// `variant="field"` sits next to an input: square, with the field's look.
const FIELD = { '--ai-radius': 'var(--hoop-field-radius)' }

export const ActionIconTheme = ActionIcon.extend({
  classNames: { root: classes.root },
  defaultProps: { size: 'md' },
  vars: (_theme, props) => {
    const shape = props.variant === 'field' ? FIELD : ROUND
    if (!SIZES.has(props.size)) return { root: shape }
    return {
      root: {
        ...shape,
        '--ai-size': `var(--hoop-control-height-${props.size})`,
      },
    }
  },
})
