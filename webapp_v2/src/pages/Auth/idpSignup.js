import { EVENTS } from '@/services/analytics'
import { authService } from '@/services/auth'
import { useUserStore } from '@/stores/useUserStore'

// CLJS :auth->get-signup-link, after its "SignUp - start signup" event.
export async function startIdpSignup() {
  useUserStore.getState().trackEvent(EVENTS.SIGNUP_START)
  const signupUrl = await authService.getSignupUrl(`${window.location.origin}/signup/callback`)
  window.location.replace(signupUrl)
}
