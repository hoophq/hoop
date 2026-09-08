import { Stack } from '@mantine/core'
import NotImplemented from '@/components/NotImplemented'
import SidecarLicenseNotice from './sections/SidecarLicenseNotice'

/**
 * The control plane landing page for every admin. Until Connecting Sidecars
 * and Resources delivers the fleet view, it carries the free-plan callout above
 * the placeholder.
 */
export default function Sidecars() {
  return (
    <Stack gap="xl">
      <SidecarLicenseNotice />
      <NotImplemented
        title="Sidecars"
        project="Connecting Sidecars and Resources"
        missing={[
          'Token issuance for a sidecar (HOOP_KEY)',
          'Resources derived from sidecar listeners',
          'Liveness — what an admin sees when a sidecar goes quiet',
        ]}
      />
    </Stack>
  )
}
