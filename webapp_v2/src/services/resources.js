import api from './api'

export const resourcesService = {
  // Paginated: { pages: { page, size, total }, data }.
  list: (params) => api.get('/resources', { params }),
}
