import { Pagination } from '@mantine/core'

// Page numbers are circles, like icon buttons: their corners come from
// --hoop-button-radius (theme.js). Theme vars run after Mantine's own, so a
// `radius` prop on a call site does nothing.
export const PaginationTheme = Pagination.extend({
  vars: () => ({ root: { '--pagination-control-radius': 'var(--hoop-button-radius)' } }),
})
