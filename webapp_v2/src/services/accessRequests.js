import api from './api'

export const accessRequestsService = {
  // Omitting `page_size` returns every rule: the handler defaults it to 0 and
  // reads 0 as "no pagination" (gateway/api/accessrequests/rules.go:214). The
  // response is the paginated envelope { pages, data } either way.
  list: () => api.get('/access-requests/rules'),
  // Rule names are user-defined and travel in the path, so every segment is
  // encoded.
  get: (name) => api.get(`/access-requests/rules/${encodeURIComponent(name)}`),
  create: (payload) => api.post('/access-requests/rules', payload),
  update: (name, payload) =>
    api.put(`/access-requests/rules/${encodeURIComponent(name)}`, payload),
  remove: (name) => api.delete(`/access-requests/rules/${encodeURIComponent(name)}`),
}
