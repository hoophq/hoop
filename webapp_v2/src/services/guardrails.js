import api from './api'

export const guardrailsService = {
  list: () => api.get('/guardrails').then((res) => res.data),
}
