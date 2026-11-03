import api from './api'

export const resourcesService = {
  // Paginated: { pages: { page, size, total }, data }.
  list: (params) => api.get('/resources', { params }),
  get: (name) => api.get('/resources/' + encodeURIComponent(name)),
  update: (name, data) => api.put('/resources/' + encodeURIComponent(name), data),
  remove: (name) => api.delete('/resources/' + encodeURIComponent(name)),
}
