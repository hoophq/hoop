import api from './api'

export const sessionsService = {
  list: (params) => api.get('/sessions', { params }),
}
