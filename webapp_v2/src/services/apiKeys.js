import api from './api'

export const apiKeysService = {
  list: () => api.get('/api-keys'),
  get: (id) => api.get(`/api-keys/${id}`),
  create: (data) => api.post('/api-keys', data),
  update: (id, data) => api.put(`/api-keys/${id}`, data),
  revoke: (id) => api.delete(`/api-keys/${id}`),
  reactivate: (id) => api.post(`/api-keys/${id}/reactivate`),
}
