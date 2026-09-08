import api from './api'

// /sidecars (gateway/api/sidecar). `version` and `last_seen_at` on a sidecar are
// gateway memory, written only by the sidecar's handshake and lost on a gateway
// restart: absent means "has not connected to this gateway process". The token
// comes back from create() once and is stored hashed; nothing returns it again.
export const sidecarsService = {
  list: () => api.get('/sidecars').then((res) => res.data ?? []),
  get: (nameOrId) => api.get(`/sidecars/${encodeURIComponent(nameOrId)}`).then((res) => res.data),
  create: (data) => api.post('/sidecars', data).then((res) => res.data),
  delete: (nameOrId) => api.delete(`/sidecars/${encodeURIComponent(nameOrId)}`),
}
