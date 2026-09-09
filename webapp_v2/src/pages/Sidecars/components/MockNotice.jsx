import { Text } from '@mantine/core'
import { FlaskConical } from 'lucide-react'
import Alert from '@/components/Alert'
import { isSidecarsMock } from '@/services/sidecars'

// Says out loud that the fleet on screen is simulated. Renders nothing when
// the real service is in use, and disappears with services/sidecars.mock.js.
export default function MockNotice() {
  if (!isSidecarsMock) return null
  return (
    <Alert color="amber" variant="light" radius="md" icon={<FlaskConical size={16} />}>
      <Text size="sm">
        {'Mock data. This fleet is simulated by '}
        <Text component="span" ff="monospace" size="sm">
          services/sidecars.mock.js
        </Text>
        {' (VITE_SIDECARS_MOCK): a new sidecar connects on its own a few seconds after it is created.'}
      </Text>
    </Alert>
  )
}
