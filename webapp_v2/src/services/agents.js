import api from './api'

export const agentsService = {
  list: () => api.get('/agents'),
  get: (id) => api.get(`/agents/${id}`),
  create: (data) => api.post('/agents', data),
  update: (id, data) => api.put(`/agents/${id}`, data),
  updateTags: (id, tags) => api.put(`/agents/${id}/tags`, tags),
  listTags: () => api.get('/agent-tags'),
  delete: (id) => api.delete(`/agents/${id}`),
}
