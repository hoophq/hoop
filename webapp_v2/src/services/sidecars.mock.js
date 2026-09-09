// MOCK — a simulated fleet for the sidecar journey while the sidecar binary
// cannot talk to the control plane yet. Same interface as the real service in
// ./sidecars.js, so the pages never know which one they got.
//
// To go live: delete this file and the VITE_SIDECARS_MOCK switch at the bottom
// of ./sidecars.js. Nothing else references it.
//
// What it simulates, in the shape the API will use:
// - POST /sidecars: 409 on a duplicate name, 422 on a reserved one, a one-time
//   hsc_ token.
// - The handshake: HANDSHAKE_AFTER_MS after creation, a sidecar "connects" —
//   last_seen_at and version appear, and so does `config`, the daemon.Config
//   the sidecar reports (sidecar/daemon/config.go: listeners, guardrails, mask,
//   analyzer, audit). The pages derive Features from that config and never
//   store their own.
// - State survives a reload (sessionStorage), so the list shows what the
//   wizard created; a new tab starts from the seed.

const STORAGE_KEY = 'hoop.sidecars.mock'
const HANDSHAKE_AFTER_MS = 8000
const LATENCY_MS = 250
const RESERVED_NAMES = ['handshake', 'configuration']

const HOUR = 60 * 60 * 1000
const now = () => Date.now()
const iso = (ms) => new Date(ms).toISOString()

// The config a connected sidecar reports. Two lanes on one postgres, one with
// every feature, one with masking only; guardrails and masking at the top
// level are the defaults a lane inherits.
function seedConfig() {
  return {
    listeners: [
      {
        name: 'dbpg-prod-agentic',
        protocol: 'postgres',
        listen: '0.0.0.0:15432',
        upstream: '10.0.1.12:5432',
        guardrails: { mode: 'enforce', rules: [{ name: 'deny-drop-table' }, { name: 'deny-delete-without-where' }] },
        mask: { rules: [{ name: 'pii-default' }] },
      },
      {
        name: 'dbpg-prod',
        protocol: 'postgres',
        listen: '0.0.0.0:15433',
        upstream: '10.0.1.12:5432',
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

// What a sidecar created in the wizard reports after its simulated handshake.
function handshakeConfig(name) {
  return {
    listeners: [
      {
        name: `${name}-pg`,
        protocol: 'postgres',
        listen: '0.0.0.0:15432',
        upstream: 'db.internal:5432',
        guardrails: { mode: 'enforce', rules: [{ name: 'deny-drop-table' }] },
        mask: { rules: [{ name: 'pii-default' }] },
      },
    ],
    guardrails: { mode: 'enforce', rules: [{ name: 'deny-drop-table' }] },
    mask: { rules: [{ name: 'pii-default' }] },
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
      connections: ['pg-prod', 'pg-prod-agentic'],
      created_by: 'admin@acme.com',
      created_at: iso(t - 7 * 24 * HOUR),
      version: '1.2.0',
      last_seen_at: iso(t - 2 * HOUR),
      config: seedConfig(),
    },
    {
      id: 'mock-edge-api',
      org_id: 'mock-org',
      name: 'edge-api',
      connections: [],
      created_by: 'admin@acme.com',
      created_at: iso(t - 3 * 24 * HOUR),
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

// The simulated handshake: a sidecar created in this session connects
// HANDSHAKE_AFTER_MS after creation, the next time anything reads it. Seeded
// sidecars keep the state they were seeded with (edge-api stays Waiting).
function settle(sidecar) {
  if (sidecar.last_seen_at || !sidecar.simulate_handshake) return sidecar
  const age = now() - new Date(sidecar.created_at).getTime()
  if (age < HANDSHAKE_AFTER_MS) return sidecar
  const { simulate_handshake: _done, ...connected } = sidecar
  return { ...connected, last_seen_at: iso(now()), version: '1.2.0', config: handshakeConfig(sidecar.name) }
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
      connections: [],
      created_by: 'you@acme.com',
      created_at: iso(now()),
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
