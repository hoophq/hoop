import api from './api'
import { mockSidecarsService } from './sidecars.mock'

// /sidecars — the control plane's fleet. `create` is the only call that
// returns the token (hsc_…); it is stored hashed and never shown again.
// `version` and `last_seen_at` are gateway memory written by the sidecar's
// handshake: absent means "has not connected to this gateway process".
// `configuration` is the daemon.Config the control plane STORES for this
// sidecar and serves back on the handshake and on every poll. Creating without
// one stores an empty document, and a sidecar with no listeners refuses to
// start. Writing it is `PUT /sidecars/:nameOrID`, which has no caller here yet:
// the pages read the configuration, they do not author it.
const apiSidecarsService = {
  list: () => api.get('/sidecars').then((r) => r.data ?? []),
  get: (nameOrId) => api.get(`/sidecars/${encodeURIComponent(nameOrId)}`).then((r) => r.data),
  create: ({ name }) => api.post('/sidecars', { name }).then((r) => r.data),
  delete: (nameOrId) => api.delete(`/sidecars/${encodeURIComponent(nameOrId)}`),
}

// MOCK SWITCH — `VITE_SIDECARS_MOCK=true npm run dev` serves the simulated
// fleet of ./sidecars.mock.js (same interface, a handshake a few seconds after
// creation) so the journey can be walked without a control plane and a running
// sidecar. Off in every build that does not set it. To go live for good:
// delete ./sidecars.mock.js, this switch and `isSidecarsMock`.
export const isSidecarsMock = import.meta.env.VITE_SIDECARS_MOCK === 'true'

export const sidecarsService = isSidecarsMock ? mockSidecarsService : apiSidecarsService
