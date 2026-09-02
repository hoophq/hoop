import { Navigate } from 'react-router-dom'
import { Stack, Text, Title } from '@mantine/core'
import { useUserStore } from '@/stores/useUserStore'

// The dead end is not a redirect: ProtectedRoute denies with <Navigate to="/">,
// so a user with neither role would bounce between the two.
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
