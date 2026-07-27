import { Box } from '@mantine/core'
import { PAGE_PADDING } from './PageLayout'

// Renders edge-to-edge and one viewport tall inside the padded PageLayout.
// `m={-PAGE_PADDING}` cancels that padding (single-sourced from PageLayout);
// the height subtracts Mantine's header-offset var (the mobile header height,
// 0 on desktop) so it fits without scrolling on both.
const FULL_BLEED_HEIGHT = 'calc(100dvh - var(--app-shell-header-offset, 0rem))'

export default function FullBleed({ children }) {
  return (
    <Box m={-PAGE_PADDING} h={FULL_BLEED_HEIGHT}>
      {children}
    </Box>
  )
}
