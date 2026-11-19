import { Accordion } from '@mantine/core'

// An accordion is a block of rows, like a card: its corners come from
// --hoop-card-radius (theme.js). Theme vars run after Mantine's own, so a
// `radius` prop on a call site does nothing.
export const AccordionTheme = Accordion.extend({
  vars: () => ({ root: { '--accordion-radius': 'var(--hoop-card-radius)' } }),
})
