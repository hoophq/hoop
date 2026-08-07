import api from './api'

export const guardrailsService = {
  list: () => api.get('/guardrails'),
  get: (id) => api.get(`/guardrails/${id}`),
  create: (data) => api.post('/guardrails', data),
  update: (id, data) => api.put(`/guardrails/${id}`, data),
  remove: (id) => api.delete(`/guardrails/${id}`),
}
