import api from './api'

const path = (id) => `/sessions/${encodeURIComponent(id)}`

export const sessionsService = {
  list: (params) => api.get('/sessions', { params }),
  // `script.data` carries the session input. A sidecar review stores the held
  // statement there, and the handler returns it whenever `expand` is absent.
  // params: expand ('event_stream,session_input'), event_stream ('base64' |
  // 'raw-queries' | 'utf8'), extension ('txt' | 'csv' | 'json', answers
  // { download_url }), 'blob-type' ('session_input').
  get: (id, params) => api.get(path(id), { params }),
  // The output of an exec above the 4 MB that `get` expands, as plain text.
  resultStream: (id) => api.get(`${path(id)}/result/stream`, { responseType: 'text' }),
  // → 202 { output_status: 'running' } when the gateway stops waiting after 50 s.
  exec: (id) => api.post(`${path(id)}/exec`),
  kill: (id) => api.post(`${path(id)}/kill`),
  // Re-run: { script, connection, labels, correlation_id?, jira_fields?, cmdb_fields? }.
  create: (payload) => api.post('/sessions', payload),
  // RDP playback and its PII analysis.
  rdpFrames: (id, params) => api.get(`${path(id)}/rdp-frames`, { params }),
  rdpDetections: (id) => api.get(`${path(id)}/rdp-detections`),
  retryRdpDetections: (id) => api.post(`${path(id)}/rdp-detections/retry`),
  // The masking report of one session: { items: [...] }.
  report: (id, startDate) => api.get('/reports/sessions', { params: { id, start_date: startDate } }),
}
