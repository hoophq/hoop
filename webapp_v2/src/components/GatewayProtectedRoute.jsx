import { useLocation } from 'react-router-dom'
import { connectionsService } from '@/services/connections'
import { agentsEnabled, useAgentsEnabled } from '@/modes/agents'
import Home from '@/pages/Home'
import { LICENSE_INTRO_PATH } from '@/utils/licenseIntro'
import ProtectedRoute from './ProtectedRoute'

// The terminal (where login lands), the home and the agent onboarding (where
// setup lands).
const isAgentLanding = (pathname) =>
  pathname === '/' ||
  pathname === '/client' ||
  (pathname.startsWith('/onboarding') && pathname !== LICENSE_INTRO_PATH)

// Without agents, an agent landing gives way to the pages a sidecar org uses
// (pages/Home: Sidecars or Reviews). Checked on every location change, not once:
// the gate stays mounted while the CLJS app navigates (its home to the
// onboarding, its onboarding to the terminal). Renders after the gate loaded
// the flags.
function WithoutAgents({ children }) {
  const { pathname } = useLocation()
  const agents = useAgentsEnabled()
  return !agents && isAgentLanding(pathname) ? <Home /> : children
}

// The gateway's gate: the shared ProtectedRoute plus the onboarding redirect.
// An admin with no connections must go through onboarding, which sets up an
// agent, so only the agents product (experimental.agents) sends anyone there.
// Skipped on the onboarding routes themselves to avoid a redirect loop. The
// control plane has no onboarding to send anyone to, so it renders
// ProtectedRoute directly.
function GatewayProtectedRoute({ children, ...props }) {
  const location = useLocation()
  const isOnboardingRoute = location.pathname.startsWith('/onboarding')

  const onReady = async (user) => {
    if (!user.is_admin || isOnboardingRoute || !agentsEnabled()) return null
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

  return (
    <ProtectedRoute {...props} onReady={onReady}>
      <WithoutAgents>{children}</WithoutAgents>
    </ProtectedRoute>
  )
}

export default GatewayProtectedRoute
