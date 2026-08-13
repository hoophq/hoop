import api from './api'

export const onboardingService = {
  // Admin-only. → { completed, checks, exec_connection_name, first_connection_name }
  get: () => api.get('/orgs/onboarding').then((r) => r.data),
}
