import { Box } from '@mantine/core'

// Exported so FullBleed can cancel exactly this padding (single source of truth).
export const PAGE_PADDING = 40

// The padded body of a React page, shared by both products. The gateway's
// command palette is mounted next to it by the gateway `Page` wrapper
// (modes/gateway.jsx), so it exists on every migrated page and never on the
// ClojureApp catch-all; the control plane mounts its own in ControlPlaneLayout.
function PageLayout({ children }) {
  return (
    <Box p={PAGE_PADDING} mih="100%">
      {children}
    </Box>
  )
}

export default PageLayout
