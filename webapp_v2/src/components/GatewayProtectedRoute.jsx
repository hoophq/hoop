import { useLocation } from 'react-router-dom'
import ProtectedRoute from './ProtectedRoute'
import { connectionsService } from '@/services/connections'

// The gateway's gate: the shared ProtectedRoute plus the onboarding redirect.
// An admin with no connections must go through onboarding. Skipped on the
// onboarding routes themselves to avoid a redirect loop. The control plane has
// no onboarding to send anyone to, so it renders ProtectedRoute directly.
function GatewayProtectedRoute(props) {
  const location = useLocation()
  const isOnboardingRoute = location.pathname.startsWith('/onboarding')

  const onReady = async (user) => {
    if (!user.is_admin || isOnboardingRoute) return null
    try {
      const { pages } = await connectionsService.getConnectionsPaginated({ pageSize: 1 })
      if ((pages?.total ?? 0) === 0) {
        // Protection rules come first: until a profile has been applied
        // (default_protection_profile is null for both "never chose" and
        // "manual"), onboarding starts at the protection-rules step.
        return user.default_protection_profile ? '/onboarding/setup' : '/onboarding/protection-rules'
      }
    } catch {
      // On API error, let the user through rather than blocking access.
    }
    return null
  }

  return <ProtectedRoute {...props} onReady={onReady} />
}

export default GatewayProtectedRoute
