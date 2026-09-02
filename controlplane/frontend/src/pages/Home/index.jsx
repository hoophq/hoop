import { Navigate } from 'react-router-dom'
import { Stack, Text, Title } from '@mantine/core'
import { useUserStore } from '@/stores/useUserStore'

/**
 * Start page. Sends each role to the first surface it can reach.
 *
 * It cannot redirect unconditionally: ProtectedRoute answers a denied route with
 * <Navigate to="/">, so a user with neither role would bounce between the two
 * until the browser throttles it.
 *
 * The dead end is the honest answer for that user. The control plane is an
 * administration surface — an end user does not authenticate with us at all,
 * they reach their resource through the sidecar.
 */
export default function Home() {
  const { isAdmin, isApprover } = useUserStore()

  if (isAdmin) return <Navigate to="/sidecars" replace />
  if (isApprover) return <Navigate to="/reviews" replace />

  return (
    <Stack mih="100vh" align="center" justify="center" gap="xs" p="xl">
      <Title order={3}>Administrators and approvers only</Title>
      <Text size="sm" c="dimmed" ta="center" maw={420}>
        This is the hoop control plane. Your resources are reached directly through the
        sidecar, not through this app.
      </Text>
    </Stack>
  )
}
