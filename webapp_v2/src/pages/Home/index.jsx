import { Navigate, useNavigate } from 'react-router-dom'
import { Stack, Text, Title } from '@mantine/core'
import Button from '@/components/Button'
import { useAuthStore } from '@/stores/useAuthStore'
import { useUserStore } from '@/stores/useUserStore'
import { ROLE_ADMIN, ROLE_APPROVER } from '@/utils/roles'

// Where each role lands. Everyone else gets the dead end below.
const HOME = { [ROLE_ADMIN]: '/sidecars', [ROLE_APPROVER]: '/reviews' }

// The control plane is an administration surface: an end user never signs in
// here, they reach their resource through the sidecar. A user with neither
// role gets the honest answer and a way out.
function NoRole() {
  const navigate = useNavigate()
  const user = useUserStore((s) => s.user)
  const logout = useAuthStore((s) => s.logout)

  const handleLogout = () => {
    logout()
    navigate('/login')
  }

  return (
    <Stack mih="60vh" align="center" justify="center" gap="xs" p="xl">
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

/**
 * The leaf of the '/' route. It cannot be a plain <Navigate> for everyone: the
 * target routes are gated, and ProtectedRoute answers a denied route with
 * <Navigate to="/">, so a user without a role would bounce between the two.
 */
export default function Home() {
  const role = useUserStore((s) => s.role)
  const target = HOME[role]
  if (target) return <Navigate to={target} replace />
  return <NoRole />
}
