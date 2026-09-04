import { Navigate, useNavigate } from 'react-router-dom'
import { Button, Stack, Text, Title } from '@mantine/core'
import { useUserStore } from '@/stores/useUserStore'
import { useAuthStore } from '@/stores/useAuthStore'

// The dead end is not a redirect: ProtectedRoute denies with <Navigate to="/">,
// so a user with neither role would bounce between the two.
export default function Home() {
  const navigate = useNavigate()
  const { isAdmin, isApprover, user } = useUserStore()
  const { logout } = useAuthStore()

  if (isAdmin) return <Navigate to="/sidecars" replace />
  if (isApprover) return <Navigate to="/reviews" replace />

  const handleLogout = () => {
    logout()
    navigate('/login')
  }

  return (
    <Stack mih="100vh" align="center" justify="center" gap="xs" p="xl">
      <Title order={3}>Administrators and approvers only</Title>
      <Text size="sm" c="dimmed" ta="center" maw={420}>
        This is the hoop control plane. Your resources are reached directly through the
        sidecar, not through this app.
      </Text>
      <Text size="sm" c="dimmed" ta="center" maw={420}>
        {user?.email
          ? `Ask an administrator to grant ${user.email} a role if you need access.`
          : 'Ask an administrator to grant your account a role if you need access.'}
      </Text>
      <Button variant="outline" color="gray" mt="md" onClick={handleLogout}>
        Sign out
      </Button>
    </Stack>
  )
}
