import { Text } from '@mantine/core'
import { TriangleAlert } from 'lucide-react'
import Alert from '@/components/Alert'
import { useUserStore } from '@/stores/useUserStore'

// Warns when this app is talking to a backend running the full gateway API.
//
// The control plane is a subset of the gateway: a gateway booted without
// APP_MODE=control-plane answers every route, including the ones a real
// control-plane deployment returns 404 for. Developing against one means a
// service call to a blocked route works locally and fails in production, which
// is the single easiest mistake to make on this app and the hardest to spot.
// A line in the README does not prevent it; this does, on first load.
//
// Dev only. In a production build the operator cannot act on it, and the
// gateway already logs the matching warning at startup.
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
        {'This backend serves the full gateway API. Routes the control plane blocks in production will work here — set APP_MODE=control-plane on the gateway to develop against the real surface.'}
      </Text>
    </Alert>
  )
}
