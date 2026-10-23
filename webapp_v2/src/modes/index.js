import { TRAFFIC_AGENT, TRAFFIC_SIDECAR } from '@/utils/ruleTraffic'

// Where an auth page sends the user next. The web terminal is the landing page
// of a signed-in user. A new local org continues in the CLJS onboarding. With
// experimental.agents off, GatewayProtectedRoute sends both to Sidecars or Reviews.
export const POST_LOGIN_PATH = '/client'
export const POST_SETUP_PATH = '/onboarding/setup'

const AGENT_AND_SIDECAR = [TRAFFIC_AGENT, TRAFFIC_SIDECAR]

// Which traffic a guardrail, masking or analyzer rule can protect. Every
// gateway serves sidecars.
export function useRuleTraffics() {
  return AGENT_AND_SIDECAR
}
