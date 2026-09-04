import { Text } from '@mantine/core'
import { TriangleAlert } from 'lucide-react'
import Alert from '@/components/Alert'
import { useUserStore } from '@/stores/useUserStore'

// Tells a developer that this app is talking to a gateway, not a control plane.
//
// The control plane serves every route the gateway serves (ADR-0013), so a
// gateway backend hides nothing from this app. The difference is what runs
// behind the routes: a gateway also starts the gRPC transport, the protocol
// proxies and the transport plugins, and the deployment this app ships
// against does not.
//
// Dev only. In a production build the operator cannot act on it.
export default function ModeBanner() {
  const applicationMode = useUserStore((state) => state.applicationMode)

  // Null while /serverinfo is in flight, and on a gateway too old to report the
  // field — neither is a reason to cry wolf.
  if (!import.meta.env.DEV || applicationMode !== 'gateway') return null

  return (
    <Alert
      color="amber"
      variant="light"
      radius={0}
      py="sm"
      icon={<TriangleAlert size={18} />}
    >
      <Text size="sm" component="span">
        {'This backend runs as the gateway. The control plane serves the same routes, so this app behaves the same here, but the real deployment starts no gRPC transport, proxies or plugins. Run make run-dev-control-plane to develop against it.'}
      </Text>
    </Alert>
  )
}
