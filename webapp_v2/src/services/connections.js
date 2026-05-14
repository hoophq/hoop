import api from './api'

export const connectionsService = {
  getConnections: () => api.get('/connections').then((res) => res.data),
}
