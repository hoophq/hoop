import api from './api'

export const sessionsService = {
  list: (params) => api.get('/sessions', { params }),
  // `script.data` carries the session input. A sidecar review stores the held
  // statement there, and the handler returns it whenever `expand` is absent.
  get: (id) => api.get(`/sessions/${encodeURIComponent(id)}`),
}
