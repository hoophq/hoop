import api from './api'

// /sidecars — the control plane's fleet. `create` is the only call that
// returns the token (hsc_…); it is stored hashed and never shown again.
// `version` and `last_seen_at` are gateway memory written by the sidecar's
// handshake: absent means "has not connected to this gateway process".
// `configuration` is the daemon.Config the control plane STORES for this
// sidecar and serves back on the handshake and on every poll. Creating without
// one stores an empty document; the sidecar then seeds the plane with its own
// config file on the first handshake (importLocalConfig, #1803), which is the
// connect journey the wizard prints. The fleet pages never author listeners;
// they only flip which side owns the document, and `patch` merges just that
// key with one `PATCH /sidecars/:nameOrID` that leaves the rest of the stored
// document alone, so a config a sidecar imports meanwhile is never clobbered.
export const sidecarsService = {
  list: () => api.get('/sidecars').then((r) => r.data ?? []),
  get: (nameOrId) => api.get(`/sidecars/${encodeURIComponent(nameOrId)}`).then((r) => r.data),
  create: ({ name }) => api.post('/sidecars', { name }).then((r) => r.data),
  patch: (nameOrId, configuration) =>
    api.patch(`/sidecars/${encodeURIComponent(nameOrId)}`, { configuration }).then((r) => r.data),
  delete: (nameOrId) => api.delete(`/sidecars/${encodeURIComponent(nameOrId)}`),
}
