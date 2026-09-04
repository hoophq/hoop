import { Navigate } from 'react-router-dom'
import { Stack, Text, Title } from '@mantine/core'
import ClojureApp from '@/components/ClojureApp'
import PageLayout from '@/layout/PageLayout'
import { useModeConfig } from '@/modes'
import { useUserStore } from '@/stores/useUserStore'

// The control plane is an administration surface: an end user never signs in
// here, they reach their resource through the sidecar. So there is no non-admin
// view to build, and the dead end is the honest answer. It sits inside the
// shell so the user menu (sign out) stays reachable.
function AdminsOnly() {
  return (
    <Stack mih="60vh" align="center" justify="center" gap="xs" p="xl">
      <Title order={3}>Administrators only</Title>
      <Text size="sm" c="dimmed" ta="center" maw={420}>
        This is the hoop control plane. Your resources are reached directly through the
        sidecar, not through this app.
      </Text>
    </Stack>
  )
}

/**
 * The leaf of the '/' route.
 *
 * Gateway: the CLJS app owns '/', as before. Control plane: admins go to the
 * mode's home. It cannot be a plain <Navigate> for everyone — that route is
 * adminOnly, and ProtectedRoute answers a denied adminOnly route with
 * <Navigate to="/">, so a non-admin would bounce between the two.
 */
export default function ModeHome() {
  const { home } = useModeConfig()
  const isAdmin = useUserStore((s) => s.isAdmin)

  if (!home) return <ClojureApp />
  if (isAdmin) return <Navigate to={home} replace />
  return (
    <PageLayout>
      <AdminsOnly />
    </PageLayout>
  )
}
