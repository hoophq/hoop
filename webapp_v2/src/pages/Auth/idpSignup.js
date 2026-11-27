import { EVENTS } from '@/services/analytics'
import { authService } from '@/services/auth'
import { useUserStore } from '@/stores/useUserStore'

const TRACK_WAIT_MS = 1000

// CLJS :auth->get-signup-link, after its "SignUp - start signup" event. Like
// CLJS, the event goes only when /serverinfo said analytics is on: a
// signed-out visitor sends nothing (/publicserverinfo has no such field).
export async function startIdpSignup() {
  const tracked = Promise.race([
    useUserStore.getState().trackEvent(EVENTS.SIGNUP_START),
    new Promise((resolve) => setTimeout(resolve, TRACK_WAIT_MS)),
  ])
  const [signupUrl] = await Promise.all([
    authService.getSignupUrl(`${window.location.origin}/signup/callback`),
    tracked,
  ])
  window.location.replace(signupUrl)
}
