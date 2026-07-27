import api from './api'

export const aiAgentsService = {
  list: () => api.get('/ai-agents'),
  get: (id) => api.get(`/ai-agents/${id}`),
  create: (data) => api.post('/ai-agents', data),
  update: (id, data) => api.put(`/ai-agents/${id}`, data),
  revoke: (id) => api.delete(`/ai-agents/${id}`),
  reactivate: (id) => api.post(`/ai-agents/${id}/reactivate`),
}
