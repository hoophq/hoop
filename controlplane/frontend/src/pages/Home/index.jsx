import { Navigate } from 'react-router-dom'
import { Stack, Text, Title } from '@mantine/core'
import { useUserStore } from '@/stores/useUserStore'

/**
 * Start page.
 *
 * It cannot be a plain <Navigate to="/sidecars">: that route is adminOnly, and
 * ProtectedRoute answers a denied adminOnly route with <Navigate to="/">, so a
 * non-admin would bounce between the two until the browser throttles it.
 *
 * The dead end is the honest answer, not a placeholder. The control plane is an
 * administration surface — per the Admin Authentication project, an end user does not
 * authenticate with us at all; they reach their resource through the sidecar, which is
 * transparent to them. So there is no non-admin view to build.
 */
export default function Home() {
  const isAdmin = useUserStore((s) => s.isAdmin)

  if (isAdmin) return <Navigate to="/sidecars" replace />

  return (
    <Stack mih="100vh" align="center" justify="center" gap="xs" p="xl">
      <Title order={3}>Administrators only</Title>
      <Text size="sm" c="dimmed" ta="center" maw={420}>
        This is the hoop control plane. Your resources are reached directly through the
        sidecar, not through this app.
      </Text>
    </Stack>
  )
}
