import api from './api'

// /sidecars — the control plane's fleet. `create` is the only call that
// returns the token (hsc_…); it is stored hashed and never shown again.
// `version`, `last_seen_at`, `served_revision`, `applied_revision` and
// `last_outcome` are written by the sidecar's handshake and stored on the row,
// so they survive a gateway restart. Absent means the sidecar has never
// handshaken, or is too old to report that field: read it as unknown, never as
// converged. `served_revision` is the configuration the plane last answered
// with and `applied_revision` the one the sidecar says it runs; equal, with a
// healthy `last_outcome`, is the only combination that means in sync.
// `configuration` is the daemon.Config the control plane STORES for this
// sidecar and serves back on the handshake and on every poll. Creating without
// one stores an empty document; the sidecar then seeds the plane with its own
// config file on the first handshake (importLocalConfig, #1803), which is the
// connect journey the wizard prints. Once the plane holds listeners that seed
// is refused with a 409 and the plane owns the document from then on.
//
// Two ways to write it, and they are not interchangeable:
//
// `patch` merges the keys it sends and leaves the rest of the stored document
// alone, so it cannot clobber a configuration a sidecar imported meanwhile.
// The fleet pages use it to flip which side owns the document.
//
// `update` REPLACES the whole document. The listener editor needs that — there
// is no per-listener endpoint, so it reads the configuration, edits one element
// of `listeners`, and writes all of it back. With no ETag, two admins editing
// at once means the second write wins silently.
export const sidecarsService = {
  list: () => api.get('/sidecars'),
  get: (nameOrId) => api.get(`/sidecars/${encodeURIComponent(nameOrId)}`),
  create: ({ name }) => api.post('/sidecars', { name }),
  patch: (nameOrId, configuration) => api.patch(`/sidecars/${encodeURIComponent(nameOrId)}`, { configuration }),
  update: (nameOrId, configuration) => api.put(`/sidecars/${encodeURIComponent(nameOrId)}`, { configuration }),
  delete: (nameOrId) => api.delete(`/sidecars/${encodeURIComponent(nameOrId)}`),
}
