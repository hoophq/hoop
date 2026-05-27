import { useConnectionsMetadataStore } from '@/stores/useConnectionsMetadataStore'

// Icon for any connection. The connection's `subtype` is the only key
// we look up — no aliases, no command-parsing fallbacks. If the
// subtype is missing from connections-metadata.json the fallback icon
// renders, which forces legacy backend rows (e.g. subtype "cloudwatch"
// rather than "aws-cloudwatch") to be corrected rather than papered
// over.
//
// The metadata store loads asynchronously at app start (App.jsx). A
// component that needs an up-to-date icon must therefore *subscribe*
// to the store so React re-renders it once the catalog arrives — use
// `useConnectionIconGetter` from inside a component body, then call
// the returned getter with each connection. For non-React contexts
// `getConnectionIcon` reads `getState()` directly and accepts the
// "may return the fallback if metadata isn't loaded yet" caveat.
const FALLBACK = '/icons/connections/custom-ssh.svg'

function iconUrlFromMetadata(connection, getIconName) {
  const iconName = getIconName(connection?.subtype)
  return iconName ? `/icons/connections/${iconName}-default.svg` : FALLBACK
}

export function getConnectionIcon(connection) {
  return iconUrlFromMetadata(
    connection,
    useConnectionsMetadataStore.getState().getIconName,
  )
}

export function useConnectionIconGetter() {
  // Subscribe to the metadata slice so consumers re-render when the
  // catalog finishes loading. Re-creating the getter on every metadata
  // change is cheap and keeps the returned function pointing at the
  // latest store snapshot.
  const metadata = useConnectionsMetadataStore((s) => s.metadata)
  const getIconName = useConnectionsMetadataStore((s) => s.getIconName)
  return (connection) =>
    iconUrlFromMetadata(connection, metadata ? getIconName : () => null)
}
