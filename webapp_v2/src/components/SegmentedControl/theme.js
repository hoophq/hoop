import { SegmentedControl } from '@mantine/core'

// A segmented control is a row of tabs: square, from --hoop-tab-radius
// (theme.js), the track and the moving indicator both. Theme vars run after
// Mantine's own, so a `radius` prop on a call site does nothing.
export const SegmentedControlTheme = SegmentedControl.extend({
  vars: () => ({ root: { '--sc-radius': 'var(--hoop-tab-radius)' } }),
})
