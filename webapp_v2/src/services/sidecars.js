import api from './api'

// /sidecars — the control plane's fleet. `create` is the only call that
// returns the token (hsc_…); it is stored hashed and never shown again.
// `version` and `last_seen_at` are gateway memory written by the sidecar's
// handshake: absent means "has not connected to this gateway process".
// `configuration` is the daemon.Config the control plane STORES for this
// sidecar and serves back on the handshake and on every poll. Creating without
// one stores an empty document; the sidecar then seeds the plane with its own
// config file on the first handshake (importLocalConfig, #1803), which is the
// connect journey the wizard prints. `update` writes it with
// `PUT /sidecars/:nameOrID`, which replaces the document whole: the fleet
// pages author no listeners, they only flip `load_from_disk`, so the caller
// reads the stored document and writes it back changed.
export const sidecarsService = {
  list: () => api.get('/sidecars').then((r) => r.data ?? []),
  get: (nameOrId) => api.get(`/sidecars/${encodeURIComponent(nameOrId)}`).then((r) => r.data),
  create: ({ name }) => api.post('/sidecars', { name }).then((r) => r.data),
  update: (nameOrId, configuration) =>
    api.put(`/sidecars/${encodeURIComponent(nameOrId)}`, { configuration }).then((r) => r.data),
  delete: (nameOrId) => api.delete(`/sidecars/${encodeURIComponent(nameOrId)}`),
}
