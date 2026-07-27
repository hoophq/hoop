import api from './api'

export const dataMaskingService = {
  list: () => api.get('/datamasking-rules'),
  get: (id) => api.get(`/datamasking-rules/${id}`),
  create: (data) => api.post('/datamasking-rules', data),
  update: (id, data) => api.put(`/datamasking-rules/${id}`, data),
  remove: (id) => api.delete(`/datamasking-rules/${id}`),
}
