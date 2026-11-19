import { Checkbox, CheckboxIndicator } from '@mantine/core'

// A checkbox is square, like a field: its corners come from
// --hoop-field-radius (theme.js). Theme vars run after Mantine's own, so a
// `radius` prop on a call site does nothing.
const SQUARE = { root: { '--checkbox-radius': 'var(--hoop-field-radius)' } }

export const CheckboxTheme = Checkbox.extend({ vars: () => SQUARE })
export const CheckboxIndicatorTheme = CheckboxIndicator.extend({ vars: () => SQUARE })
