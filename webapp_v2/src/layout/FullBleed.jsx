import { Box } from '@mantine/core'
import { PAGE_PADDING } from './PageLayout'

// Renders edge-to-edge and one viewport tall inside the padded PageLayout.
// `m={-PAGE_PADDING}` cancels that padding (single-sourced from PageLayout);
// the height subtracts Mantine's header-offset var — the global header, 56px on
// every breakpoint — so it fits without scrolling. The var is absent on routes
// rendered outside Layout, where this falls back to a full viewport height.
const FULL_BLEED_HEIGHT = 'calc(100dvh - var(--app-shell-header-offset, 0rem))'

export default function FullBleed({ children }) {
  return (
    <Box m={-PAGE_PADDING} h={FULL_BLEED_HEIGHT}>
      {children}
    </Box>
  )
}
