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
  ssh: { type: 'application', subtype: 'ssh' },
  // Roles keep the subtype httpproxy; the metadata entry says web-application.
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
  // Manual and Secrets Manager use the metadata form; IAM Federation its own.
  'bigquery',
  // Its metadata has HOST and PORT, both required, as resources/constants.cljs.
  'tcp',
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

// The subtypes with a form of their own, not built from the metadata.
const ROLE_FORMS = {
  ssh: 'ssh',
  'linux-vm': 'linux-vm',
  httpproxy: 'httpproxy',
  grafana: 'httpproxy',
  kibana: 'httpproxy',
  'claude-code': 'claude-code',
  mcp: 'mcp',
  mcpproxy: 'mcpproxy',
}

const MCP_GATEWAY_FLAG = 'experimental.mcp_gateway'

// The CLJS catalog shows mcpproxy only with the MCP Gateway flag, and mcp only
// without it.
export function hiddenByFlags(catalogId, featureFlags = {}) {
  const gateway = Boolean(featureFlags[MCP_GATEWAY_FLAG])
  return (catalogId === 'mcpproxy' && !gateway) || (catalogId === 'mcp' && gateway)
}

export const REACT_SETUP_SUBTYPES = new Set([
  ...METADATA_DRIVEN_SUBTYPES,
  ...DEV_ENV_SUBTYPES,
  ...Object.keys(ROLE_FORMS),
])

// One of the ROLE_FORMS values, or 'metadata' for the metadata-driven form.
export function roleFormFor(subtype) {
  return ROLE_FORMS[subtype] ?? 'metadata'
}

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

// /resources/:name/add-role adds roles of the resource's own type and subtype.
// Linux VM is the only one with a command the metadata does not carry.
export function setupConfigForResource(resource) {
  const mapped = Object.values(SETUP_MAPPINGS).find((m) => m.subtype === resource.subtype && m.command)
  return { type: resource.type, subtype: resource.subtype, command: mapped?.command ?? null }
}

export function isReactSetup(config) {
  return REACT_SETUP_SUBTYPES.has(config.subtype)
}

// The metadata entry of a subtype: the CLJS wizard reads its fields, name and
// command by subtype, not by catalog id. httpproxy is found by its id (Q22).
export function metadataForSubtype(connections = [], subtype) {
  return (
    connections.find((c) => c.resourceConfiguration?.subtype === subtype) ??
    connections.find((c) => c.id === subtype) ??
    null
  )
}
