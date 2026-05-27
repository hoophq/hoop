import api from './api'

export const connectionTagsService = {
  list: () => api.get('/connection-tags').then((res) => res.data),
}
