import { useLocation } from 'react-router-dom'
import { isAgentPath } from '@/modes/agentPaths'
import { useAgentsEnabled } from '@/modes/agents'
import Home from '@/pages/Home'
import ProtectedRoute from './ProtectedRoute'

// Without agents, an agent page gives way to the pages a sidecar org uses
// (pages/Home: Sidecars or Reviews). Checked on every location change, not once:
// the gate stays mounted while the CLJS app navigates (its home to the
// onboarding, its onboarding to the terminal). Renders after the gate loaded
// the flags.
function WithoutAgents({ children }) {
  const { pathname } = useLocation()
  const agents = useAgentsEnabled()
  return !agents && isAgentPath(pathname) ? <Home /> : children
}

// The gateway's gate: the shared ProtectedRoute plus the agents swap.
// Onboarding is reached only from the initial setup (POST_SETUP_PATH) or by
// navigating to it; no redirect sends an admin there.
function GatewayProtectedRoute({ children, ...props }) {
  return (
    <ProtectedRoute {...props}>
      <WithoutAgents>{children}</WithoutAgents>
    </ProtectedRoute>
  )
}

export default GatewayProtectedRoute
