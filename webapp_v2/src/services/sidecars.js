import api from './api'

// /sidecars — the control plane's fleet. `create` is the only call that
// returns the token (hsc_…); it is stored hashed and never shown again.
// `version` and `last_seen_at` are gateway memory written by the sidecar's
// handshake: absent means "has not connected to this gateway process".
// `configuration` is the daemon.Config the control plane STORES for this
// sidecar and serves back on the handshake and on every poll. Creating without
// one stores an empty document; the sidecar then seeds the plane with its own
// config file on the first handshake (importLocalConfig, #1803), which is the
// connect journey the wizard prints. Writing it from here is
// `PUT /sidecars/:nameOrID`, which has no caller: the pages read the
// configuration, they do not author it.
export const sidecarsService = {
  list: () => api.get('/sidecars'),
  get: (nameOrId) => api.get(`/sidecars/${encodeURIComponent(nameOrId)}`),
  create: ({ name }) => api.post('/sidecars', { name }),
  delete: (nameOrId) => api.delete(`/sidecars/${encodeURIComponent(nameOrId)}`),
}
