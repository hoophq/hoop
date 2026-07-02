import { Box } from '@mantine/core'
import ConnectedCommandPalette from '@/features/CommandPalette'

// Exported so FullBleed can cancel exactly this padding (single source of truth).
export const PAGE_PADDING = 40

// PageLayout is only used by React-owned routes (the CLJS catch-all renders
// <Layout><ClojureApp /></Layout> without PageLayout). Mounting the Mantine
// Spotlight here guarantees cmd+K works on every migrated page without having
// to maintain a parallel list of React route patterns.
function PageLayout({ children }) {
  return (
    <Box p={PAGE_PADDING} mih="100%">
      {children}
      <ConnectedCommandPalette />
    </Box>
  )
}

export default PageLayout
