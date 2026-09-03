import { useEffect } from 'react'
import { Stack } from '@mantine/core'
import NotImplemented from '@/components/NotImplemented'
import { useUserStore } from '@/stores/useUserStore'
import SidecarLicenseNotice from './components/SidecarLicenseNotice'

// A read from ProtectedRoute or the focus refetch inside this window is fresh
// enough; the landing page must not add a request to every visit.
const MOUNT_REFRESH_MAX_AGE_MS = 15_000

/**
 * The landing page for every admin. Until Connecting Sidecars and Resources
 * delivers the fleet view, it carries the Enterprise-only notice above the
 * placeholder, so the first thing an unlicensed admin reads is the way in.
 */
export default function Sidecars() {
  const refreshServerInfo = useUserStore((state) => state.refreshServerInfo)

  useEffect(() => {
    const { serverInfoFetchedAt } = useUserStore.getState()
    if (Date.now() - serverInfoFetchedAt > MOUNT_REFRESH_MAX_AGE_MS) refreshServerInfo()
  }, [refreshServerInfo])

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
