// What the resources seed writes: a resource per type family of the setup
// wizard, roles shaped as the wizard and the gateway store them, and AWS
// Connect jobs. Every id derives from a name and every timestamp is fixed, so
// two runs write the same rows.
import { SIDECAR, seedId, users as sessionUsers } from '../seed-sessions/catalog.mjs'

export { SIDECAR }

export const AT = '2026-09-01T00:00:00Z'
export const AGENTS_FLAG = 'experimental.agents'

// The admin is the sessions seed's admin; the standard user is in no group.
export function users({ adminGroup }) {
  return {
    admin: sessionUsers({ adminGroup }).admin,
    standard: { email: 'standard@seed.example.com', name: 'Seed Standard', groups: [] },
  }
}

export const ATTRIBUTES = [
  { name: 'seed-prod', description: 'Production data' },
  { name: 'seed-pii', description: 'Holds personal data' },
]

// The gateway's default for a postgres role with no command
// (gateway/api/connections/helpers.go GetConnectionDefaults).
const PSQL = ['psql', '-v', 'ON_ERROR_STOP=1', '-A', '-F\t', '-P', 'pager=off', '-h', '$HOST', '-U', '$USER',
  '--port=$PORT', '$DB']
const BQ = ['bq', 'query', '--use_legacy_sql=false', '--format=prettyjson', '--project_id=$CLOUDSDK_CORE_PROJECT']
const pg = (user) => ({
  'envvar:HOST': 'seed-pg.internal', 'envvar:PORT': '5432', 'envvar:USER': user, 'envvar:PASS': 'not-a-password',
  'envvar:DB': 'orders',
})
const SA_JSON = JSON.stringify({
  type: 'service_account', project_id: 'seed-project', client_email: 'seed-admin@seed-project.iam.gserviceaccount.com',
  private_key: 'not-a-key',
})

// The body the wizard sends to PUT /connections/:role/federation
// (webapp/src/webapp/resources/federation/events.cljs). The gateway encrypts
// admin_credentials_json with its own key, so the seed sends it through the API.
export const FEDERATION = {
  hook_source: 'builtin', builtin_provider: 'gcp_iam', extra_config: { project_id: 'seed-project' },
  identity_source_attribute: '$.user.email', identity_target_template: '{user.email}', fallback_policy: 'deny',
  token_ttl_seconds: 3600, admin_credentials_json: SA_JSON,
}

// A resource per family of the inventory §4, then the ones the matrix needs.
// A role takes its resource's type and subtype, unless it says otherwise.
// envs hold plain values; the seed stores them base64, as the wizard sends them.
export const RESOURCES = [
  {
    name: 'seed-res-postgres', type: 'database', subtype: 'postgres', family: 'Metadata-driven database',
    roles: [
      { name: 'seed-res-postgres-ro', command: PSQL, envs: pg('reader'), tags: { env: 'prod', team: 'data' },
        attributes: ['seed-pii', 'seed-prod'] },
      { name: 'seed-res-postgres-rw', command: PSQL, envs: pg('writer'), tags: { env: 'staging' },
        attributes: ['seed-prod'] },
      { name: 'seed-res-postgres-iam', command: PSQL,
        envs: { ...pg('_aws_iam_rds:hoop_iam'), 'envvar:PASS': '_aws_iam_rds:authtoken' } },
    ],
  },
  {
    name: 'seed-res-python', type: 'custom', subtype: 'python', family: 'Dev env',
    roles: [{ name: 'seed-res-python', command: ['python3', '-'] }],
  },
  {
    name: 'seed-res-ssh', type: 'application', subtype: 'ssh', family: 'SSH',
    roles: [
      { name: 'seed-res-ssh-password', command: [],
        envs: { 'envvar:HOST': 'seed-ssh.internal', 'envvar:PORT': '22', 'envvar:USER': 'ubuntu', 'envvar:PASS': 'not-a-password' } },
      { name: 'seed-res-ssh-key', command: [],
        envs: { 'envvar:HOST': 'seed-ssh.internal', 'envvar:PORT': '22', 'envvar:USER': 'ubuntu',
          'envvar:AUTHORIZED_SERVER_KEYS': '-----BEGIN OPENSSH PRIVATE KEY-----\nnot-a-key\n-----END OPENSSH PRIVATE KEY-----' } },
    ],
  },
  {
    name: 'seed-res-tcp', type: 'application', subtype: 'tcp', family: 'TCP',
    roles: [{ name: 'seed-res-tcp', command: [], envs: { 'envvar:HOST': 'seed-redis.internal', 'envvar:PORT': '6379' } }],
  },
  {
    // The wizard drops the catalog's ["bash"] (quirk Q17).
    name: 'seed-res-linux-vm', type: 'custom', subtype: 'linux-vm', family: 'Linux VM',
    roles: [{ name: 'seed-res-linux-vm', command: [], envs: { 'envvar:APP_ENV': 'staging' } }],
  },
  {
    name: 'seed-res-http', type: 'httpproxy', subtype: 'httpproxy', family: 'HTTP proxy',
    roles: [{ name: 'seed-res-http', command: [],
      envs: { 'envvar:REMOTE_URL': 'https://seed-api.internal', 'envvar:INSECURE': 'false',
        'envvar:HEADER_Authorization': 'Bearer not-a-token' } }],
  },
  {
    name: 'seed-res-claude-code', type: 'httpproxy', subtype: 'claude-code', family: 'Claude Code',
    roles: [{ name: 'seed-res-claude-code', command: [],
      envs: { 'envvar:REMOTE_URL': 'https://api.anthropic.com', 'envvar:HEADER_X_API_KEY': 'sk-ant-not-a-key',
        'envvar:INSECURE': 'false' } }],
  },
  {
    // HEADER_AUTHORIZATION is what the OAuth popup writes; no grant row.
    name: 'seed-res-mcp', type: 'httpproxy', subtype: 'mcp', family: 'MCP',
    roles: [{ name: 'seed-res-mcp', command: [],
      envs: { 'envvar:REMOTE_URL': 'https://seed-mcp.example.com/mcp', 'envvar:INSECURE': 'false',
        'envvar:HEADER_AUTHORIZATION': 'Bearer not-a-token' } }],
  },
  {
    name: 'seed-res-mcp-gateway', type: 'httpproxy', subtype: 'mcpproxy', family: 'MCP Gateway',
    roles: [{ name: 'seed-res-mcp-gateway', command: [],
      envs: { 'envvar:REMOTE_URL': 'https://gitlab.com/api/v4/mcp', 'envvar:MCP_TRANSPORT': 'streamable-http',
        'envvar:MCP_AUTH': 'static', 'envvar:HEADER_Authorization': 'Bearer not-a-token', 'envvar:INSECURE': 'false' } }],
  },
  {
    name: 'seed-res-bigquery', type: 'custom', subtype: 'bigquery', family: 'BigQuery with federation',
    roles: [
      { name: 'seed-res-bigquery-federated', command: BQ, federation: FEDERATION,
        envs: { 'envvar:CLOUDSDK_CORE_PROJECT': 'seed-project', 'filesystem:GOOGLE_APPLICATION_CREDENTIALS': SA_JSON } },
      { name: 'seed-res-bigquery-static', command: BQ,
        envs: { 'envvar:CLOUDSDK_CORE_PROJECT': 'seed-project', 'filesystem:GOOGLE_APPLICATION_CREDENTIALS': SA_JSON } },
    ],
  },
  {
    // More than the 50 a page of /resources/configure/:id roles holds.
    name: 'seed-res-many', type: 'database', subtype: 'postgres',
    roles: Array.from({ length: 55 }, (_, i) => ({
      name: `seed-res-many-${String(i + 1).padStart(2, '0')}`, command: PSQL, envs: pg(`user${i + 1}`),
    })),
  },
  { name: 'seed-res-no-roles', type: 'database', subtype: 'mysql', roles: [] },
  {
    // A connection an agent declares and syncs (connectionsync.go): its own
    // resource, managed_by hoopagent, schema enabled whatever its type.
    name: 'seed-agent-managed', type: 'custom', subtype: '',
    roles: [{ name: 'seed-agent-managed', command: ['bash'], managedBy: 'hoopagent', accessSchema: 'enabled' }],
  },
  {
    // A mirror of the sidecar's listener appdb, as the projection writes it
    // (migration 000129). No list shows it.
    name: 'seed-sidecar-appdb', type: 'database', subtype: 'postgres', agent: false, hidden: true,
    roles: [{ name: 'seed-sidecar-appdb', managedBy: 'sidecar', sidecar: { listener: 'appdb' }, access: 'disabled',
      accessSchema: 'disabled' }],
  },
]

// Every role, with the defaults POST /resources applies.
export function roles() {
  return RESOURCES.flatMap((r) => r.roles.map((role) => ({
    type: r.type,
    subtype: r.subtype,
    access: 'enabled',
    accessSchema: r.type === 'database' ? 'enabled' : 'disabled',
    ...role,
    resource: r.name,
  })))
}

// AWS Connect jobs, as the gateway stores them: a result's state is under
// "phase" (gateway/models/dbroles.go), its completed_at is RFC 3339.
const job = (key, dbName, fields) => ({
  key,
  id: seedId('dbrolejob', key),
  spec: {
    account_arn: 'arn:aws:iam::123456789012:user/hoop', user_id: 'AIDASEEDEXAMPLE',
    db_arn: `arn:aws:rds:us-east-1:123456789012:db:${dbName}`, db_name: dbName, db_engine: 'postgres',
    db_tags: [{ env: 'seed' }],
  },
  hook_status: null,
  ...fields,
})
const role = (userRole, phase, completedAt, message = '') => ({
  user_role: userRole, phase, message, completed_at: completedAt,
  credentials_info: { secrets_manager_provider: 'database', secret_id: '', secret_keys: [] },
})

export const JOBS = [
  job('running', 'billing-staging', {
    created_at: '2026-10-03T10:00:00Z', completed_at: null,
    status: { phase: 'running', message: '', result: null },
  }),
  job('completed', 'orders-prod', {
    created_at: '2026-10-02T14:03:00Z', completed_at: '2026-10-02T14:03:12Z',
    status: { phase: 'completed', message: 'All user roles have been successfully provisioned', result: [
      role('hoop_ro', 'completed', '2026-10-02T14:03:10Z'),
      role('hoop_rw', 'completed', '2026-10-02T14:03:11Z'),
      role('hoop_ddl', 'completed', '2026-10-02T14:03:12Z'),
    ] },
    hook_status: { exit_code: 0, output: Buffer.from('ok\n').toString('base64'), execution_time_sec: 2 },
  }),
  job('failed', 'analytics-dev', {
    created_at: '2026-10-01T09:00:00Z', completed_at: '2026-10-01T09:00:06Z',
    status: { phase: 'failed', message: 'One or more user roles failed to be provisioned', result: [
      role('hoop_ro', 'completed', '2026-10-01T09:00:05Z'),
      role('hoop_rw', 'failed', '2026-10-01T09:00:06Z', 'pq: permission denied to create role'),
    ] },
  }),
]
