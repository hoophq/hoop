import api from './api'

export const featureFlagsService = {
  list: () => api.get('/feature-flags'),
  update: (name, data) => api.put(`/feature-flags/${name}`, data),
}
