import { Navigate } from 'react-router-dom'
import { useUserStore } from '@/stores/useUserStore'
import { ROLE_ADMIN } from '@/utils/roles'

/**
 * The leaf of the '/' route. An admin lands on Sidecars. Everyone else is a
 * reviewer: their groups come from the identity provider at login, and Reviews
 * is the page they act on.
 */
export default function Home() {
  const role = useUserStore((s) => s.role)
  return <Navigate to={role === ROLE_ADMIN ? '/sidecars' : '/reviews'} replace />
}
