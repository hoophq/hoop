// MOCK — a simulated fleet for the sidecar journey, so the pages can be walked
// without a control plane and a running sidecar. Same interface as the real
// service in ./sidecars.js, so the pages never know which one they got.
//
// To go live: delete this file and the VITE_SIDECARS_MOCK switch at the bottom
// of ./sidecars.js. Nothing else references it.
//
// What it simulates, in the shape the API uses (openapi.SidecarResponse):
// - POST /sidecars: 409 on a duplicate name, 422 on a reserved one, a one-time
//   hsc_ token, and an EMPTY `configuration` — the real endpoint stores one
//   only if the request carries it, and this UI has no editor to author it.
// - The handshake: HANDSHAKE_AFTER_MS after creation a sidecar "calls home",
//   so `version` and `last_seen_at` appear. The configuration does not: the
//   control plane holds that document (PUT /sidecars/:nameOrID) and serves it,
//   the sidecar never reports one.
// - The seeded fleet covers both states a row can be in: checked in, and
//   never checked in (see ../pages/Sidecars/status.js).
// - State survives a reload (sessionStorage); a new tab starts from the seed.

const STORAGE_KEY = 'hoop.sidecars.mock'
const HANDSHAKE_AFTER_MS = 8000
const LATENCY_MS = 250
const RESERVED_NAMES = ['handshake', 'configuration']

const HOUR = 60 * 60 * 1000
const now = () => Date.now()
const iso = (ms) => new Date(ms).toISOString()

// A configuration an admin stored for this sidecar: two lanes on one postgres,
// one with every feature, one with masking only. Top-level guardrails and mask
// are the defaults a lane inherits when it declares none of its own.
function seedConfiguration(name, upstream) {
  return {
    listeners: [
      {
        name: `${name}-agentic`,
        protocol: 'postgres',
        listen: '0.0.0.0:15432',
        upstream,
        guardrails: { mode: 'enforce', rules: [{ name: 'deny-drop-table' }, { name: 'deny-delete-without-where' }] },
        mask: { rules: [{ name: 'pii-default' }] },
      },
      {
        name,
        protocol: 'postgres',
        listen: '0.0.0.0:15433',
        upstream,
        guardrails: { mode: 'observe', rules: [] },
        mask: { rules: [{ name: 'pii-default' }] },
      },
    ],
    guardrails: { mode: 'enforce', rules: [{ name: 'deny-drop-table' }] },
    mask: { rules: [{ name: 'pii-default' }] },
    analyzer: { provider: 'anthropic' },
    audit: { file: '-', redact_statements: false },
    log_level: 'info',
  }
}

function seed() {
  const t = now()
  return [
    {
      id: 'mock-dbpg-prod',
      org_id: 'mock-org',
      name: 'dbpg-prod',
      created_by: 'admin@acme.com',
      created_at: iso(t - 7 * 24 * HOUR),
      configuration: seedConfiguration('dbpg-prod', '10.0.1.12:5432'),
      version: '1.2.0',
      // A check-in a moment ago: Connected.
      last_seen_at: iso(t - 40 * 1000),
    },
    {
      // Registered, never started: Waiting, and nothing to show yet.
      id: 'mock-edge-api',
      org_id: 'mock-org',
      name: 'edge-api',
      created_by: 'admin@acme.com',
      created_at: iso(t - 3 * 24 * HOUR),
      configuration: {},
    },
  ]
}

function load() {
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY)
    if (raw) return JSON.parse(raw)
  } catch {
    // A blocked or corrupt sessionStorage falls back to the seed.
  }
  const fleet = seed()
  save(fleet)
  return fleet
}

function save(fleet) {
  try {
    sessionStorage.setItem(STORAGE_KEY, JSON.stringify(fleet))
  } catch {
    // Nothing to do: the in-memory fleet still works for this page.
  }
}

// The simulated handshake: a sidecar created in this session calls home
// HANDSHAKE_AFTER_MS after creation, the next time anything reads it. It
// reports its version and nothing else — the gateway records that much before
// it answers with the stored configuration. Seeded sidecars keep the state
// they were seeded with (edge-api stays Waiting).
function settle(sidecar) {
  if (sidecar.last_seen_at || !sidecar.simulate_handshake) return sidecar
  const age = now() - new Date(sidecar.created_at).getTime()
  if (age < HANDSHAKE_AFTER_MS) return sidecar
  const { simulate_handshake: _done, ...connected } = sidecar
  return { ...connected, last_seen_at: iso(now()), version: '1.2.0' }
}

function settleAll(fleet) {
  const settled = fleet.map(settle)
  if (settled.some((s, i) => s !== fleet[i])) save(settled)
  return settled
}

function apiError(status, message) {
  const err = new Error(message)
  err.response = { status, data: { message } }
  return err
}

const delay = (value) => new Promise((resolve) => setTimeout(() => resolve(value), LATENCY_MS))
const fail = (err) => new Promise((_, reject) => setTimeout(() => reject(err), LATENCY_MS))

function token() {
  const bytes = crypto.getRandomValues(new Uint8Array(32))
  const b64 = btoa(String.fromCharCode(...bytes)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
  return `hsc_${b64}`
}

// Strip the fields the real API never returns on list/get.
const publicView = (sidecar) => {
  const view = { ...sidecar }
  delete view.simulate_handshake
  return view
}

export const mockSidecarsService = {
  list: () => delay(settleAll(load()).map(publicView)),

  get: (nameOrId) => {
    const found = settleAll(load()).find((s) => s.id === nameOrId || s.name === nameOrId)
    return found ? delay(publicView(found)) : fail(apiError(404, 'sidecar not found'))
  },

  create: ({ name }) => {
    const fleet = load()
    if (RESERVED_NAMES.includes(name)) return fail(apiError(422, `name "${name}" is reserved`))
    if (fleet.some((s) => s.name === name)) return fail(apiError(409, 'a sidecar with this name already exists'))
    const sidecar = {
      id: `mock-${name}-${fleet.length + 1}`,
      org_id: 'mock-org',
      name,
      created_by: 'you@acme.com',
      created_at: iso(now()),
      configuration: {},
      simulate_handshake: true,
    }
    save([...fleet, sidecar])
    return delay({ ...sidecar, token: token() })
  },

  delete: (nameOrId) => {
    const fleet = load()
    const next = fleet.filter((s) => s.id !== nameOrId && s.name !== nameOrId)
    if (next.length === fleet.length) return fail(apiError(404, 'sidecar not found'))
    save(next)
    return delay(undefined)
  },
}
