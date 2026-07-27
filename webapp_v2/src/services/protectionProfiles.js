import api from './api'

export const protectionProfilesService = {
  // GET /orgs/protection-profile → { profile: string|null, attribute_name: string|null }
  get: () => api.get('/orgs/protection-profile').then((r) => r.data),
  // PUT /orgs/protection-profile — profile null means manual configuration.
  // source is required by the API for analytics: 'onboarding' | 'settings'
  update: ({ profile, source }) =>
    api.put('/orgs/protection-profile', { profile, source }).then((r) => r.data),
}
