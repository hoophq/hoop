import { useLocation } from 'react-router-dom'
import { useAgentsEnabled } from '@/modes/agents'
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

// The gateway's gate: the shared ProtectedRoute plus the agents landing swap.
// Onboarding is reached only from the initial setup (postSetupPath) or by
// navigating to it; no redirect sends an admin there.
function GatewayProtectedRoute({ children, ...props }) {
  return (
    <ProtectedRoute {...props}>
      <WithoutAgents>{children}</WithoutAgents>
    </ProtectedRoute>
  )
}

export default GatewayProtectedRoute
