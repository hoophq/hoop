// /resources/new?type=<catalog id> names a catalog entry. setupConfigFor turns
// the id into what the wizard creates, as get-setup-config does in CLJS
// (connections/views/resource_catalog/helpers.cljs): this map first, else the
// entry's resourceConfiguration. Catalog ids are not subtypes
// (aws-ssm-script → aws-ssm), so the metadata must be loaded.
const SETUP_MAPPINGS = {
  postgres: { type: 'database', subtype: 'postgres' },
  mysql: { type: 'database', subtype: 'mysql' },
  mongodb: { type: 'database', subtype: 'mongodb' },
  mssql: { type: 'database', subtype: 'mssql' },
  oracle: { type: 'database', subtype: 'oracledb' },
  ssh: { type: 'application', subtype: 'ssh' },
  tcp: { type: 'application', subtype: 'tcp' },
  httpproxy: { type: 'httpproxy', subtype: 'httpproxy' },
  // A catalog-only entry: it is not in the metadata.
  'linux-vm': { type: 'custom', subtype: 'linux-vm', command: ['bash'] },
}

// The subtypes the React wizard creates. Every other one renders the CLJS
// wizard until its family ticket adds it here.
const METADATA_DRIVEN_SUBTYPES = [
  'postgres',
  'mysql',
  'mssql',
  'oracledb',
  'mongodb',
  'cassandra',
  'redis',
  'dynamodb',
  'aws-cli',
  'aws-cloudwatch',
  'aws-ecs',
  'aws-ssm',
  'kubernetes',
  'kubernetes-eks',
  'rdp',
  'git',
  'github',
]

const DEV_ENV_SUBTYPES = [
  'nodejs',
  'python',
  'clojure',
  'django',
  'elixir',
  'php-artisan',
  'ruby-on-rails',
]

export const REACT_SETUP_SUBTYPES = new Set([...METADATA_DRIVEN_SUBTYPES, ...DEV_ENV_SUBTYPES])

// { type, subtype, command } for a catalog id, or null when the id names
// nothing. `command` is null when the entry carries none.
export function setupConfigFor(catalogId, connections = []) {
  if (!catalogId) return null
  const mapped = SETUP_MAPPINGS[catalogId]
  if (mapped) return { command: null, ...mapped }
  const config = connections.find((c) => c.id === catalogId)?.resourceConfiguration
  if (!config?.type || !config?.subtype) return null
  return { type: config.type, subtype: config.subtype, command: config.command ?? null }
}

export function isReactSetup(config) {
  return REACT_SETUP_SUBTYPES.has(config.subtype)
}

// The metadata entry of a subtype: the CLJS wizard reads its fields, name and
// command by subtype, not by catalog id.
export function metadataForSubtype(connections = [], subtype) {
  return connections.find((c) => c.resourceConfiguration?.subtype === subtype) ?? null
}
