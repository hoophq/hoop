import { Box } from '@mantine/core'
import ConnectedCommandPalette from '@/features/CommandPalette'

// Exported so FullBleed can cancel exactly this padding (single source of truth).
export const PAGE_PADDING = 40

// Mounting the Mantine Spotlight here guarantees cmd+K works on every page that
// uses the app chrome, without maintaining a parallel list of route patterns.
function PageLayout({ children }) {
  return (
    <Box p={PAGE_PADDING} mih="100%">
      {children}
      <ConnectedCommandPalette />
    </Box>
  )
}

export default PageLayout
