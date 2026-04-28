import api from './api'

export const attributesService = {
  list: () => api.get('/attributes'),
  get: (name) => api.get(`/attributes/${name}`),
  create: (data) => api.post('/attributes', data),
  update: (name, data) => api.put(`/attributes/${name}`, data),
  remove: (name) => api.delete(`/attributes/${name}`),
}
