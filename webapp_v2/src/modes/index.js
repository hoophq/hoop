import { useAuthStore } from '@/stores/useAuthStore'
import { samePagePath } from '@/utils/redirect'
import { TRAFFIC_AGENT, TRAFFIC_SIDECAR } from '@/utils/ruleTraffic'

// Where an auth page sends the user next. The web terminal is the landing page
// of a signed-in user. A new local org continues in the CLJS onboarding. With
// experimental.agents off, GatewayProtectedRoute sends both to Sidecars or Reviews.
export const POST_LOGIN_PATH = '/client'
export const POST_SETUP_PATH = '/onboarding/setup'

// Sends a person who just signed up back to the page that sent them to auth
// (ProtectedRoute saved it), as Auth/Callback does after a login, else to
// fallback. Only a page of this app, in place: a saved URL that points
// elsewhere is dropped. Cleared either way, so it never replays later.
export function goAfterAuth(navigate, fallback = POST_LOGIN_PATH, options) {
  const target = samePagePath(useAuthStore.getState().getAndClearRedirectUrl())
  if (target) {
    navigate(target, { ...options, replace: true })
    return
  }
  navigate(fallback, options)
}

const AGENT_AND_SIDECAR = [TRAFFIC_AGENT, TRAFFIC_SIDECAR]

// Which traffic a guardrail, masking or analyzer rule can protect. Every
// gateway serves sidecars.
export function useRuleTraffics() {
  return AGENT_AND_SIDECAR
}
