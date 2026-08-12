import api from './api'

export const onboardingService = {
  // GET /orgs/onboarding → { completed, checks: {...}, exec_connection_name,
  // first_connection_name }. Admin-only. The gateway computes every check in a
  // single query — do not go back to deriving the checklist client-side, that
  // fan-out is what made the widget hammer GET /sessions (DEP-136).
  get: () => api.get('/orgs/onboarding').then((r) => r.data),
}
