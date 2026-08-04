import api from './api'

// The gateway clamps `limit` at 100 (gateway/api/session/session.go
// `defaultMaxSessionListLimit`). Asking for more silently returns 100 — never
// assume the response honoured the requested page size.
export const MAX_LIMIT = 100

export const sessionsService = {
  // NOTE: returns the RAW axios response, unlike every other method here.
  // `useConfigStatusStore` destructures `{ data }` off the response and reads
  // `data.total`. Unwrapping this would not throw — it would silently pin the
  // sidebar Config Status "session ran" check to false forever. Leave it raw.
  list: (params) => api.get('/sessions', { params }),

  get: (id, params) =>
    api.get(`/sessions/${encodeURIComponent(id)}`, { params }).then((r) => r.data),

  // Port of :audit->get-filtered-sessions-by-id (webapp events/audit.cljs:69).
  // GET /sessions has no `id` param, so this is N detail fetches. Three
  // deliberate differences from the CLJS original:
  //  - no `?event_stream=base64`: nothing a list row renders comes from the
  //    event stream, and CLJS downloaded a full base64 transcript per session
  //  - no 1000ms `dispatch-later`: every dispatch carried the same fixed delay,
  //    so it was a uniform penalty, not a stagger
  //  - results keep INPUT order; CLJS prepended each arrival, so what the user
  //    saw was HTTP-race order
  getFilteredByIds: async (ids = []) => {
    if (!ids.length) return { sessions: [], failedIds: [] }
    const results = await Promise.allSettled(ids.map((id) => sessionsService.get(id)))
    const sessions = []
    const failedIds = []
    results.forEach((result, i) => {
      if (result.status === 'fulfilled' && result.value?.id) sessions.push(result.value)
      else failedIds.push(ids[i])
    })
    return { sessions, failedIds }
  },

  // Port of :audit->get-sessions-by-batch-id (events/audit.cljs:118 and :132),
  // which built the query string by concatenation and never encoded batch_id.
  getByBatchId: (batchId, { limit = 20, offset = 0 } = {}) =>
    api
      .get('/sessions', { params: { batch_id: batchId, limit, offset } })
      .then((r) => r.data),

  // Port of :audit->kill-session (events/audit.cljs:603).
  kill: (id) =>
    api.post(`/sessions/${encodeURIComponent(id)}/kill`).then((r) => r.data),

  // Port of :audit->execute-session (events/audit.cljs:340). A 202 with
  // output_status "running" means the gateway gave up waiting after 50s and the
  // caller must poll.
  exec: (id) =>
    api.post(`/sessions/${encodeURIComponent(id)}/exec`).then((r) => r.data),

  // Re-run: v1 creates a NEW session rather than re-executing the old one
  // (events/audit.cljs:438-442 builds { script, labels.re-run-from, connection }).
  create: (payload) => api.post('/sessions', payload).then((r) => r.data),

  // :audit->get-session-logs-data (events/audit.cljs:624) — feeds the Logs tab
  // of the asciinema view.
  getLogs: (id) =>
    api
      .get(`/sessions/${encodeURIComponent(id)}`, {
        params: { expand: 'event_stream,session_input', event_stream: 'base64' },
      })
      .then((r) => r.data),

  // --- Wave 6 (session details) consumers -----------------------------------

  // :audit->get-session-stream-result (events/audit.cljs:308)
  streamResult: (id) =>
    api.get(`/sessions/${encodeURIComponent(id)}/result/stream`).then((r) => r.data),

  // :audit->session-file-generate (events/audit.cljs:580). Not a byte download:
  // the gateway answers { download_url, expire_at } and the caller opens it.
  // Dedicated /sessions/:id/download and /download/input routes do exist
  // (gateway/api/server.go:814-815) but CLJS never used them — Wave 6 should
  // choose deliberately rather than inherit this.
  downloadFile: (id, extension) =>
    api
      .get(`/sessions/${encodeURIComponent(id)}`, { params: { extension } })
      .then((r) => r.data),

  // :audit->session-input-download (events/audit.cljs:597). The param really is
  // `blob-type` with a hyphen — that is what the gateway parses.
  downloadInput: (id) =>
    api
      .get(`/sessions/${encodeURIComponent(id)}`, {
        params: { 'blob-type': 'session_input', extension: 'txt' },
      })
      .then((r) => r.data),
}
