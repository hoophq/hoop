import { Tabs } from '@mantine/core'

// Every tab takes its corners from --hoop-tab-radius (theme.js): square, so
// the hover fill and the focus ring are square too. Theme vars run after
// Mantine's own, so a `radius` prop on a call site does nothing.
export const TabsTheme = Tabs.extend({
  vars: () => ({ root: { '--tabs-radius': 'var(--hoop-tab-radius)' } }),
})
