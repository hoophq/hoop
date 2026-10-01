import api from './api'

export const sessionsService = {
  list: (params) => api.get('/sessions', { params }),
  // `script.data` is the input: a sidecar review's held statement, sent when `expand` is absent.
  // For a sidecar review, `labels` carry the filer (sidecar.requester.*).
  get: (id) => api.get(`/sessions/${encodeURIComponent(id)}`),
}
