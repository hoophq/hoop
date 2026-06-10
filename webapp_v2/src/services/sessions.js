import api from './api'

export const sessionsService = {
  // Supported params: user, connection, type, 'review.status',
  // start_date / end_date (RFC3339), limit (max 100), offset.
  list: (params = {}) => api.get('/sessions', { params }),
  // Loads the session input by default. Never pass expand=event_stream from
  // app screens — the event stream blob can be arbitrarily large.
  get: (id) => api.get(`/sessions/${id}`),
  kill: (id) => api.post(`/sessions/${id}/kill`),
}
