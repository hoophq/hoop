import api from './api'

export const attributesService = {
  // Paginated: defaults to page 1 with 50 rows, `page_size` caps at 100.
  list: (params) => api.get('/attributes', { params }),
  // Attribute names are user-defined, so every path segment is encoded.
  get: (name) => api.get(`/attributes/${encodeURIComponent(name)}`),
  create: (data) => api.post('/attributes', data),
  update: (name, data) => api.put(`/attributes/${encodeURIComponent(name)}`, data),
  remove: (name) => api.delete(`/attributes/${encodeURIComponent(name)}`),
}
