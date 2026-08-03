import api from './api'

export const attributesService = {
  // Paginated: defaults to page 1 with 50 rows, `page_size` caps at 100.
  list: (params) => api.get('/attributes', { params }),
  get: (name) => api.get(`/attributes/${name}`),
  create: (data) => api.post('/attributes', data),
  update: (name, data) => api.put(`/attributes/${name}`, data),
  remove: (name) => api.delete(`/attributes/${name}`),
}
