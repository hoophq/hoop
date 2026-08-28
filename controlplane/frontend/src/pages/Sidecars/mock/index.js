/* ────────────────────────────────────────────────────────────────────────────
 * THE ONLY FAKE THING ON THIS PAGE. DELETE THIS DIRECTORY WHEN THE API LANDS.
 *
 * Removal, three steps:
 *   1. rm -rf src/pages/Sidecars/mock
 *   2. store.js   — drop the `fleetMock` import, call `sidecarsService.fleet()`
 *   3. index.jsx  — drop the `MockBanner` import and its one line
 *
 * Nothing else in pages/Sidecars/ knows the data is fake. The row component,
 * the store shape, the state vocabulary and the helpers are all real and stay.
 *
 * The banner lives in here on purpose: deleting the mock deletes the warning
 * with it, so the page can never quietly ship looking real while it is not.
 *
 * THE SHAPE BELOW IS A GUESS. `GET /api/fleet` is EVL-232 and its response is
 * not specified yet. `lanes[]` follows the sidecar's own `GET /config`, which
 * is the closest thing to a contract that exists today. When EVL-232 settles,
 * the shape here is what has to change — not the components.
 * ──────────────────────────────────────────────────────────────────────────── */

const MINUTE = 60 * 1000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

// Relative to load time so the column never looks frozen.
const ago = (ms) => new Date(Date.now() - ms).toISOString()

const FLEET = [
  {
    name: 'pg-prod-01',
    state: 'connected',
    version: '1.42.0',
    generation: { issued: 7, applied: 7 },
    last_seen: ago(12 * 1000),
    reason: null,
    lanes: [
      {
        name: 'appdb',
        connection: 'appdb',
        protocol: 'postgres',
        upstream: 'db.internal:5432',
        enforcing: true,
        masking: true,
        rules: ['no-destructive-sql', 'no-cpf-in-query'],
      },
      {
        name: 'billing-api',
        connection: 'billing-api',
        protocol: 'http',
        upstream: 'billing.internal:8080',
        enforcing: true,
        masking: false,
        rules: ['no-admin-api'],
      },
    ],
  },
  {
    name: 'pg-prod-02',
    state: 'connected',
    version: '1.42.0',
    generation: { issued: 7, applied: 7 },
    last_seen: ago(31 * 1000),
    reason: null,
    lanes: [
      {
        name: 'appdb',
        connection: 'appdb',
        protocol: 'postgres',
        upstream: 'db-replica.internal:5432',
        enforcing: true,
        masking: true,
        rules: ['no-destructive-sql', 'no-cpf-in-query'],
      },
      {
        name: 'reporting',
        connection: 'reporting',
        protocol: 'mssql',
        upstream: 'mssql.internal:1433',
        enforcing: true,
        masking: true,
        rules: ['no-destructive-tsql'],
      },
      // The exception the collapsed row has to surface. Rolling out is the
      // right reason to be here; staying is not.
      {
        name: 'warehouse',
        connection: 'warehouse',
        protocol: 'postgres',
        upstream: 'warehouse.internal:5432',
        enforcing: false,
        masking: false,
        rules: ['no-destructive-sql'],
      },
    ],
  },
  {
    name: 'billing-eu',
    state: 'stale',
    version: '1.41.2',
    generation: { issued: 7, applied: 6 },
    last_seen: ago(4 * MINUTE),
    reason: null,
    lanes: [
      {
        name: 'billing-db',
        connection: 'billing-db',
        protocol: 'postgres',
        upstream: 'billing-db.eu:5432',
        enforcing: true,
        masking: true,
        rules: ['no-destructive-sql'],
      },
    ],
  },
  {
    name: 'analytics-01',
    state: 'rejected',
    version: '1.42.0',
    generation: { issued: 8, applied: 7 },
    last_seen: ago(45 * 1000),
    // The highest-value field in the whole view. A rejected row without it
    // sends the operator to the logs.
    reason: 'rule "pii-guard" names entity BR_CPF, which the detector is not configured to find',
    lanes: [
      {
        name: 'events',
        connection: 'events',
        protocol: 'postgres',
        upstream: 'events.internal:5432',
        enforcing: true,
        masking: true,
        rules: ['no-destructive-sql'],
      },
    ],
  },
  // Two per-user pods fronting the SAME connection. This is the case a
  // sidecar-keyed page cannot answer: "which sidecars serve appdb?" means
  // opening every row. Left in the mock so the limit is visible, not theoretical.
  {
    name: 'pod-alice',
    state: 'connected',
    version: '1.42.0',
    generation: { issued: 7, applied: 7 },
    last_seen: ago(8 * 1000),
    reason: null,
    lanes: [
      {
        name: 'appdb',
        connection: 'appdb',
        protocol: 'postgres',
        upstream: 'db.internal:5432',
        enforcing: true,
        masking: true,
        rules: ['no-destructive-sql', 'no-cpf-in-query'],
      },
    ],
  },
  {
    name: 'pod-bob',
    state: 'connected',
    version: '1.41.2',
    generation: { issued: 7, applied: 7 },
    last_seen: ago(3 * MINUTE),
    reason: null,
    lanes: [
      {
        name: 'appdb',
        connection: 'appdb',
        protocol: 'postgres',
        upstream: 'db.internal:5432',
        enforcing: true,
        masking: true,
        rules: ['no-destructive-sql', 'no-cpf-in-query'],
      },
    ],
  },
  {
    name: 'legacy-mssql',
    state: 'disconnected',
    version: '1.38.4',
    generation: { issued: 5, applied: 5 },
    last_seen: ago(3 * DAY),
    reason: null,
    lanes: [
      {
        name: 'erp',
        connection: 'erp',
        protocol: 'mssql',
        upstream: 'erp.internal:1433',
        enforcing: true,
        masking: false,
        rules: ['no-destructive-tsql'],
      },
    ],
  },
]

// Matches the axios shape the services return, so the store body does not
// change when the real call replaces this one.
export function fleetMock() {
  return new Promise((resolve) => {
    setTimeout(() => resolve({ data: FLEET }), 400)
  })
}
