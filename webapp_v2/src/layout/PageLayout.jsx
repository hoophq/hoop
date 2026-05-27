import { Box } from '@mantine/core'
import ConnectedCommandPalette from '@/features/CommandPalette'

// PageLayout is only used by React-owned routes (the CLJS catch-all renders
// <Layout><ClojureApp /></Layout> without PageLayout). Mounting the Mantine
// Spotlight here guarantees cmd+K works on every migrated page without having
// to maintain a parallel list of React route patterns.
function PageLayout({ children }) {
  return (
    <Box p={40} mih="100%">
      {children}
      <ConnectedCommandPalette />
    </Box>
  )
}

export default PageLayout
