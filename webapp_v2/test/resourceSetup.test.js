import { readFileSync } from 'node:fs'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const fixture = (name) =>
  JSON.parse(readFileSync(new URL(`./fixtures/resourceSetup/${name}.json`, import.meta.url), 'utf8'))
const { connections } = fixture('metadata')

const agentsService = { list: vi.fn(), create: vi.fn() }
const resourcesService = { get: vi.fn(), create: vi.fn(), remove: vi.fn() }
const connectionsService = { list: vi.fn(), deleteConnection: vi.fn() }
const federationService = { save: vi.fn(), test: vi.fn() }
const showSnackbar = vi.fn()

vi.mock('@/services/agents', () => ({ agentsService }))
vi.mock('@/services/resources', () => ({ resourcesService }))
vi.mock('@/services/attributes', () => ({
  attributesService: { listAll: vi.fn(async () => []), create: vi.fn(async () => ({})) },
}))
vi.mock('@/services/protectionProfiles', () => ({
  protectionProfilesService: { get: vi.fn(async () => ({ profile: null, attribute_name: null })) },
}))
vi.mock('@/services/connections', () => ({ connectionsService }))
vi.mock('@/services/federation', () => ({ federationService }))
vi.mock('@/services/guardrails', () => ({ guardrailsService: { list: vi.fn() } }))
vi.mock('@/services/dataMasking', () => ({ dataMaskingService: { list: vi.fn() } }))
vi.mock('@/services/aiSessionAnalyzer', () => ({
  aiSessionAnalyzerService: { listRules: vi.fn(), getProvider: vi.fn() },
}))
vi.mock('@/utils/snackbar', () => ({ showSnackbar }))

const { setupConfigFor, isReactSetup, metadataForSubtype, hiddenByFlags } = await import(
  '@/pages/Resources/Setup/catalog'
)
const { defaultAuthMode } = await import('@/pages/Roles/Configure/utils/mcpGateway')
const { useResourceSetupStore } = await import('@/pages/Resources/Setup/store')
const { buildCreatePayload } = await import('@/pages/Resources/Setup/payload')
const { secretOf } = await import('@/pages/Resources/Setup/roleCredentials')
const { buildCards, ctaPath } = await import('@/pages/Resources/Setup/activation')
const { jsonCredentialToField } = await import('@/utils/connectionsMetadataMapper')
const { decodeSecretValue, encodeSecretForSource, sourceFromEncodedValue, SOURCES } = await import(
  '@/pages/Roles/Configure/utils/secretsCodec'
)
const { CONNECTION_METHODS } = await import('@/utils/connectionPolicy')

const PROFILE = { profile: 'protection-permissive', attribute_name: 'hoop_protection_profile-protection_permissive' }
const store = () => useResourceSetupStore.getState()
const role = (index) => store().roles[index]

// Types a value the way PredefinedFields does in the wizard: the field's
// source, AWS IAM on USER and PASS, files always manual.
function type(index, env, value) {
  const { credentials, method, provider } = role(index)
  const field = metadataForSubtype(connections, store().setup.subtype)
    .resourceConfiguration.credentials.map(jsonCredentialToField)
    .find((f) => f.envVar === env)
  const envKey = field.envKey || `envvar:${field.key.toUpperCase()}`
  const { stagedSecrets, fieldSources, replaceSecret } = credentials.getState()
  const staged = stagedSecrets[envKey]
  const defaultSource = method === CONNECTION_METHODS.SECRETS_MANAGER ? provider : SOURCES.MANUAL
  let source = fieldSources[envKey] || (staged?.value ? sourceFromEncodedValue(staged.value) : null) || defaultSource
  if (method === CONNECTION_METHODS.AWS_IAM && ['user', 'pass'].includes(field.key)) source = SOURCES.AWS_IAM_ROLE
  if (envKey.startsWith('filesystem:')) source = SOURCES.MANUAL
  replaceSecret(envKey, encodeSecretForSource(value, source))
}

// PredefinedFields stages PASS while the role is in AWS IAM mode.
function awsIamPass(index) {
  role(index).credentials.getState().replaceSecret(
    'envvar:PASS',
    encodeSecretForSource('authtoken', SOURCES.AWS_IAM_ROLE),
  )
}

function begin(catalogId, { name, agentId, profile = PROFILE }) {
  store().start(setupConfigFor(catalogId, connections))
  store().setName(name)
  store().selectAgent(agentId)
  store().goToRoles()
  useResourceSetupStore.setState({ profile })
}

// Picks IAM Federation and fills the form the way FederationSection does.
function federate(index, { oauth = false, json, project }) {
  store().setRoleMethod(role(index).id, CONNECTION_METHODS.IAM_FEDERATION)
  const credentials = role(index).credentials.getState()
  if (oauth) credentials.setFederationProvider('gcp_oauth')
  credentials.setFederationForm({ admin_credentials_json: json })
  credentials.setFederationProjectId(project)
}

const decoded = (secret) =>
  Object.fromEntries(Object.entries(secret).map(([k, v]) => [k, decodeSecretValue(v)]))

function payload() {
  const { name, setup, agentId, roles, profile } = store()
  return buildCreatePayload({
    name,
    setup,
    agentId,
    roles,
    connections,
    profileAttribute: profile?.attribute_name ?? null,
  })
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('setupConfigFor', () => {
  it.each([
    ['postgres', { type: 'database', subtype: 'postgres', command: null }],
    ['oracledb', { type: 'database', subtype: 'oracledb', command: null }],
    ['aws-ssm-script', { type: 'custom', subtype: 'aws-ssm', command: ['aws-ssm.sh'] }],
    ['remote-desktop', { type: 'custom', subtype: 'rdp', command: [] }],
    ['kubernetes-token', { type: 'custom', subtype: 'kubernetes', command: ['bash'] }],
    ['python-scripts', { type: 'custom', subtype: 'python', command: ['python3', '-'] }],
    ['linux-vm', { type: 'custom', subtype: 'linux-vm', command: ['bash'] }],
    ['oracle', { type: 'database', subtype: 'oracledb', command: null }],
    ['tcp', { type: 'application', subtype: 'tcp', command: null }],
  ])('resolves %s', (id, config) => {
    expect(setupConfigFor(id, connections)).toEqual(config)
  })

  it('answers null for no id or an unknown one (Q16)', () => {
    expect(setupConfigFor(null, connections)).toBeNull()
    expect(setupConfigFor('', connections)).toBeNull()
    expect(setupConfigFor('not-a-catalog-id', connections)).toBeNull()
  })

  it('resolves a mapped id before the metadata loads', () => {
    expect(setupConfigFor('linux-vm', undefined)).toEqual({ type: 'custom', subtype: 'linux-vm', command: ['bash'] })
    expect(setupConfigFor('aws-ssm-script', undefined)).toBeNull()
  })
})

describe('isReactSetup', () => {
  it.each([
    ['postgres', true],
    ['oracledb', true],
    ['aws-ssm-script', true],
    ['remote-desktop', true],
    ['kubernetes-token', true],
    ['python-scripts', true],
    ['ruby-on-rails', true],
    ['ssh', true],
    ['webapps-and-apis', true],
    ['httpproxy', true],
    ['grafana', true],
    ['linux-vm', true],
    ['claude-code', true],
    ['mcp', true],
    ['mcpproxy', true],
    ['bigquery', true],
  ])('%s renders React: %s', (id, react) => {
    expect(isReactSetup(setupConfigFor(id, connections))).toBe(react)
  })
})

describe('hiddenByFlags', () => {
  const gateway = { 'experimental.mcp_gateway': true }
  it.each([
    ['mcpproxy', {}, true],
    ['mcpproxy', gateway, false],
    ['mcp', {}, false],
    ['mcp', gateway, true],
    ['claude-code', gateway, false],
  ])('%s with %j: %s', (id, flags, hidden) => {
    expect(hiddenByFlags(id, flags)).toBe(hidden)
  })
})

// The fixtures are POST /resources bodies the CLJS wizard sent on origin/main
// for the same input, recorded through the UI.
describe('POST /resources matches the CLJS wizard', () => {
  it('postgres, Manual, an attribute and the profile pill', () => {
    begin('postgres', { name: 'pg-manual', agentId: '7b16ea93-62fc-5077-a50c-4f441ba5d28a' })
    store().setRoleName(role(0).id, 'pg-manual-ro')
    store().setRoleAttributes(role(0).id, ['pii'], false)
    type(0, 'HOST', 'db.internal')
    type(0, 'USER', 'app')
    type(0, 'PASS', 's3cret')
    type(0, 'PORT', '5432')
    type(0, 'DB', 'app')
    type(0, 'SSLMODE', 'disable')
    expect(payload()).toEqual(fixture('postgres-manual'))
  })

  it('mysql, AWS IAM, a file, the profile pill removed', () => {
    begin('mysql', { name: 'mysql-iam', agentId: '7b16ea93-62fc-5077-a50c-4f441ba5d28a' })
    store().setRoleName(role(0).id, 'mysql-iam-rw')
    store().setRoleAttributes(role(0).id, [], true)
    store().setRoleMethod(role(0).id, CONNECTION_METHODS.AWS_IAM)
    awsIamPass(0)
    type(0, 'HOST', 'mysql.internal')
    type(0, 'USER', 'iam_user')
    type(0, 'PORT', '3306')
    type(0, 'DB', 'app')
    type(0, 'PRE_SQL_SCRIPT_FILE', "SET SESSION sql_mode='ANSI';")
    expect(payload()).toEqual(fixture('mysql-aws-iam'))
  })

  it('postgres, Secrets Manager on Vault KV v2, one field Manual', () => {
    begin('postgres', { name: 'pg-vault', agentId: '7b16ea93-62fc-5077-a50c-4f441ba5d28a' })
    store().setRoleName(role(0).id, 'pg-vault-ro')
    store().setRoleMethod(role(0).id, CONNECTION_METHODS.SECRETS_MANAGER)
    store().setRoleProvider(role(0).id, SOURCES.VAULT_KV2)
    type(0, 'HOST', 'secret/data/pg:HOST')
    type(0, 'USER', 'secret/data/pg:USER')
    type(0, 'PASS', 'secret/data/pg:PASS')
    type(0, 'DB', 'app')
    role(0).credentials.getState().setFieldSource('envvar:PORT', SOURCES.MANUAL)
    type(0, 'PORT', '5432')
    expect(payload()).toEqual(fixture('postgres-vault-kv2'))
  })

  it('aws-ssm, two roles', () => {
    begin('aws-ssm-script', { name: 'ssm', agentId: '7b16ea93-62fc-5077-a50c-4f441ba5d28a' })
    store().setRoleName(role(0).id, 'ssm-ops')
    type(0, 'AWS_ACCESS_KEY_ID', 'AKIA1')
    type(0, 'AWS_SECRET_ACCESS_KEY', 's1')
    type(0, 'AWS_REGION', 'us-east-1')
    type(0, 'INSTANCE_ID', 'i-123')
    store().addRole()
    store().setRoleName(role(1).id, 'ssm-ro')
    type(1, 'AWS_ACCESS_KEY_ID', 'AKIA2')
    type(1, 'AWS_SECRET_ACCESS_KEY', 's2')
    type(1, 'AWS_REGION', 'us-west-2')
    type(1, 'INSTANCE_ID', 'i-456')
    expect(payload()).toEqual(fixture('aws-ssm-two-roles'))
  })

  it('a dev env: no credentials, the catalog command', () => {
    begin('python-scripts', { name: 'py', agentId: '7b16ea93-62fc-5077-a50c-4f441ba5d28a' })
    store().setRoleName(role(0).id, 'py-run')
    expect(payload()).toEqual(fixture('python'))
  })

  it('a new agent', () => {
    begin('postgres', { name: 'pg-new-agent', agentId: '140c2a87-5dee-544d-b738-63f6c1975167' })
    store().setRoleName(role(0).id, 'pg-new-agent-ro')
    type(0, 'HOST', 'db2.internal')
    type(0, 'USER', 'app')
    type(0, 'PASS', 'p')
    type(0, 'PORT', '5432')
    type(0, 'DB', 'app')
    expect(payload()).toEqual(fixture('postgres-new-agent'))
  })
})

// Types into a field of the SSH, HTTP proxy or Linux VM form the way they do:
// the role's default source, files always manual.
function typeKey(index, envKey, value) {
  const { credentials, method, provider } = role(index)
  const secretsManager = method === CONNECTION_METHODS.SECRETS_MANAGER
  const source = secretsManager && !envKey.startsWith('filesystem:') ? provider : SOURCES.MANUAL
  credentials.getState().replaceSecret(envKey, encodeSecretForSource(value, source))
}

// An env var, header or file row: the form adds a placeholder row, the admin
// names it, then types its value.
function row(index, placeholder, name, value) {
  const { replaceSecret, renameSecret } = role(index).credentials.getState()
  replaceSecret(placeholder, '')
  if (name) renameSecret(placeholder, name)
  typeKey(index, placeholder, value)
}

describe('POST /resources matches the CLJS wizard: SSH, TCP, Linux VM, HTTP proxy', () => {
  const agentId = '2d653520-5060-5ec6-81dd-082934b1949d'

  it('ssh, password', () => {
    begin('ssh', { name: 'ssh-pw', agentId, profile: null })
    store().setRoleName(role(0).id, 'ssh-pw-admin')
    typeKey(0, 'envvar:HOST', 'ssh.internal')
    typeKey(0, 'envvar:PORT', '2222')
    typeKey(0, 'envvar:USER', 'ubuntu')
    typeKey(0, 'envvar:PASS', 'pw')
    expect(payload()).toEqual(fixture('ssh-password'))
  })

  it('ssh, private key on Vault KV v1', () => {
    begin('ssh', { name: 'ssh-key', agentId, profile: null })
    store().setRoleName(role(0).id, 'ssh-key-ops')
    store().setRoleMethod(role(0).id, CONNECTION_METHODS.SECRETS_MANAGER)
    typeKey(0, 'envvar:HOST', 'secret/ssh:HOST')
    typeKey(0, 'envvar:USER', 'secret/ssh:USER')
    typeKey(0, 'envvar:AUTHORIZED_SERVER_KEYS', 'secret/ssh:KEY')
    expect(payload()).toEqual(fixture('ssh-key-vault'))
  })

  it('ssh: an auth switch drops the credential the form hides', () => {
    begin('ssh', { name: 'ssh-switch', agentId, profile: null })
    typeKey(0, 'envvar:HOST', 'ssh.internal')
    typeKey(0, 'envvar:USER', 'ubuntu')
    typeKey(0, 'envvar:PASS', 'pw')
    role(0).credentials.getState().chooseSshAuth('key')
    typeKey(0, 'envvar:AUTHORIZED_SERVER_KEYS', 'KEY')
    expect(Object.keys(secretOf(role(0).credentials))).toEqual([
      'envvar:HOST',
      'envvar:USER',
      'envvar:AUTHORIZED_SERVER_KEYS',
    ])
    role(0).credentials.getState().chooseSshAuth('password')
    expect(Object.keys(secretOf(role(0).credentials))).toEqual(['envvar:HOST', 'envvar:USER'])
  })

  it('ssh, local: sent as ssh-local with no secret', () => {
    begin('ssh', { name: 'ssh-local', agentId, profile: null })
    store().setRoleName(role(0).id, 'ssh-local-sh')
    role(0).credentials.getState().setDraft({ subtype: 'ssh-local' })
    expect(payload()).toEqual(fixture('ssh-local'))
  })

  it('tcp, from the metadata form', () => {
    begin('webapps-and-apis', { name: 'tcp', agentId, profile: null })
    store().setRoleName(role(0).id, 'tcp-web')
    type(0, 'HOST', 'web.internal')
    type(0, 'PORT', '8080')
    expect(payload()).toEqual(fixture('tcp'))
  })

  it('linux-vm: an env var, a file and the arguments; unnamed rows are left out', () => {
    begin('linux-vm', { name: 'vm', agentId, profile: null })
    store().setRoleName(role(0).id, 'vm-run')
    row(0, 'envvar:NEW_KEY_1', 'envvar:API_KEY', 'k1')
    row(0, 'envvar:NEW_KEY_2', null, 'no-name')
    row(0, 'filesystem:NEW_FILE_1', 'filesystem:KUBECONFIG', 'apiVersion: v1')
    role(0).credentials.getState().setDraft({ command: ['python3', '-m', 'http.server'] })
    expect(payload()).toEqual(fixture('linux-vm'))
  })

  it('linux-vm with no arguments keeps ["bash"] (Q17)', () => {
    begin('linux-vm', { name: 'vm-default', agentId, profile: null })
    store().setRoleName(role(0).id, 'vm-default-sh')
    const cljs = fixture('linux-vm-default')
    expect(cljs.roles[0].command).toEqual([])
    expect(payload()).toEqual({ ...cljs, roles: [{ ...cljs.roles[0], command: ['bash'] }] })
  })

  it('httpproxy: a header keeps its case, insecure on', () => {
    begin('httpproxy', { name: 'web', agentId, profile: null })
    store().setRoleName(role(0).id, 'web-api')
    typeKey(0, 'envvar:REMOTE_URL', 'https://api.internal')
    row(0, 'envvar:HEADER_NEW_HEADER_1', 'envvar:HEADER_X-Api-Key', 'tok')
    role(0).credentials.getState().replaceSecret('envvar:INSECURE', encodeSecretForSource('true', SOURCES.MANUAL))
    expect(payload()).toEqual(fixture('httpproxy'))
  })

  it('grafana on Vault KV v2: INSECURE is sent without a prefix', () => {
    begin('grafana', { name: 'graf', agentId, profile: null })
    store().setRoleName(role(0).id, 'graf-ro')
    store().setRoleMethod(role(0).id, CONNECTION_METHODS.SECRETS_MANAGER)
    store().setRoleProvider(role(0).id, SOURCES.VAULT_KV2)
    typeKey(0, 'envvar:REMOTE_URL', 'https://grafana.internal')
    row(0, 'envvar:HEADER_NEW_HEADER_1', 'envvar:HEADER_Authorization', 'secret/data/grafana:TOKEN')
    expect(payload()).toEqual(fixture('grafana-vault'))
  })

  it('appends a newline to the SSH_PRIVATE_KEY file, as config->json does', () => {
    begin('linux-vm', { name: 'vm-key', agentId, profile: null })
    row(0, 'filesystem:NEW_FILE_1', 'filesystem:SSH_PRIVATE_KEY', 'KEY')
    expect(secretOf(role(0).credentials)).toEqual({
      'filesystem:SSH_PRIVATE_KEY': encodeSecretForSource('KEY\n', SOURCES.MANUAL),
    })
  })
})

// What the Claude Code, MCP and MCP Gateway renderers stage: settings plain,
// fields through typeKey. Fixtures recorded from CLJS on origin/main.
function setting(index, envKey, value) {
  role(index).credentials.getState().replaceSecret(envKey, encodeSecretForSource(value, SOURCES.MANUAL))
}

describe('POST /resources matches the CLJS wizard: Claude Code, MCP, MCP Gateway', () => {
  const agentId = '75122bce-f957-49eb-a812-2ab60977cd9f'
  const start = (catalogId, name) => {
    begin(catalogId, { name, agentId, profile: null })
    store().setRoleName(role(0).id, `${name}-role`)
  }

  it('claude-code, Anthropic, with a header', () => {
    start('claude-code', 'cc')
    typeKey(0, 'envvar:HEADER_X_API_KEY', 'sk-ant-1')
    row(0, 'envvar:HEADER_NEW_HEADER_1', 'envvar:HEADER_X-Org', 'acme')
    expect(payload()).toEqual(fixture('claude-code'))
  })

  it('claude-code, Secrets Manager: the default URL stays a value (Q39)', () => {
    start('claude-code', 'cc-vault')
    store().setRoleMethod(role(0).id, CONNECTION_METHODS.SECRETS_MANAGER)
    typeKey(0, 'envvar:HEADER_X_API_KEY', 'secret/claude:KEY')
    const cljs = fixture('claude-code-vault')
    const secret = { ...cljs.roles[0].secret, 'envvar:REMOTE_URL': btoa('https://api.anthropic.com') }
    expect(payload()).toEqual({ ...cljs, roles: [{ ...cljs.roles[0], secret }] })
  })

  it('Secrets Manager: a URL the admin typed or sourced takes the provider (Q39)', () => {
    start('claude-code', 'cc-vault')
    typeKey(0, 'envvar:REMOTE_URL', 'secret/claude:URL')
    store().setRoleMethod(role(0).id, CONNECTION_METHODS.SECRETS_MANAGER)
    expect(secretOf(role(0).credentials)['envvar:REMOTE_URL']).toBe(btoa('_vaultkv1:secret/claude:URL'))

    start('claude-code', 'cc-vault')
    role(0).credentials.getState().setFieldSource('envvar:REMOTE_URL', SOURCES.VAULT_KV1)
    store().setRoleMethod(role(0).id, CONNECTION_METHODS.SECRETS_MANAGER)
    expect(secretOf(role(0).credentials)['envvar:REMOTE_URL']).toBe(btoa('_vaultkv1:https://api.anthropic.com'))
  })

  it('mcpproxy, Secrets Manager: a catalog URL stays a value (Q39)', () => {
    start('mcpproxy', 'gw-c7')
    role(0).credentials.getState().setFormValue('envvar:REMOTE_URL', 'https://mcp.context7.com/mcp')
    store().setRoleMethod(role(0).id, CONNECTION_METHODS.SECRETS_MANAGER)
    expect(secretOf(role(0).credentials)['envvar:REMOTE_URL']).toBe(btoa('https://mcp.context7.com/mcp'))
  })

  it('claude-code, Vertex: the URL from the region, every field manual', () => {
    start('claude-code', 'cc-vertex')
    const { cancelSecretChange } = role(0).credentials.getState()
    cancelSecretChange('envvar:REMOTE_URL')
    setting(0, 'envvar:GCP_REGION', 'europe-west1')
    setting(0, 'envvar:GCP_PROJECT_ID', 'p1')
    setting(0, 'envvar:GCP_SERVICE_ACCOUNT_JSON', '{"type":"service_account"}')
    setting(0, 'envvar:REMOTE_URL', 'https://europe-west1-aiplatform.googleapis.com')
    expect(payload()).toEqual(fixture('claude-code-vertex'))
  })

  it('mcp, with a header and insecure SSL', () => {
    start('mcp', 'mcp')
    typeKey(0, 'envvar:REMOTE_URL', 'https://mcp.internal/mcp')
    row(0, 'envvar:HEADER_NEW_HEADER_1', 'envvar:HEADER_X-Team', 'core')
    setting(0, 'envvar:INSECURE', 'true')
    expect(payload()).toEqual(fixture('mcp'))
  })

  it('mcp, an OAuth token: no flow id on the role', () => {
    start('mcp', 'mcp')
    typeKey(0, 'envvar:REMOTE_URL', 'https://mcp.internal/mcp')
    setting(0, 'envvar:HEADER_AUTHORIZATION', 'Bearer t')
    role(0).credentials.getState().setMcpOAuthFlowId('flow-1')
    const [sent] = payload().roles
    expect(sent.secret['envvar:HEADER_AUTHORIZATION']).toBe(btoa('Bearer t'))
    expect(sent).not.toHaveProperty('mcp_oauth_flow_id')
  })

  it('mcpproxy, custom server, static token and a header', () => {
    start('mcpproxy', 'gw-custom')
    typeKey(0, 'envvar:REMOTE_URL', 'https://mcp.internal/mcp')
    typeKey(0, 'envvar:HEADER_Authorization', 'tok-1')
    row(0, 'envvar:HEADER_NEW_HEADER_1', 'envvar:HEADER_X-Team', 'core')
    expect(payload()).toEqual(fixture('mcpproxy-custom'))
  })

  it('mcpproxy, context7: the header name the catalog names, case kept', () => {
    start('mcpproxy', 'gw-c7')
    setting(0, 'envvar:REMOTE_URL', 'https://mcp.context7.com/mcp')
    typeKey(0, 'envvar:HEADER_CONTEXT7_API_KEY', 'c7')
    expect(payload()).toEqual(fixture('mcpproxy-context7'))
  })

  it('mcpproxy, a server with no auth on SSE', () => {
    start('mcpproxy', 'gw-none')
    setting(0, 'envvar:MCP_TRANSPORT', 'sse')
    setting(0, 'envvar:REMOTE_URL', 'https://docs.mcp.cloudflare.com/sse')
    setting(0, 'envvar:MCP_AUTH', 'none')
    expect(payload()).toEqual(fixture('mcpproxy-none'))
  })

  it('mcpproxy, github with a denied-tools policy', () => {
    start('mcpproxy', 'gw-gh')
    setting(0, 'envvar:REMOTE_URL', 'https://api.githubcopilot.com/mcp')
    typeKey(0, 'envvar:HEADER_Authorization', 'ghp-1')
    setting(0, 'envvar:MCP_DENIED_TOOLS', 'delete_*')
    expect(payload()).toEqual(fixture('mcpproxy-github-policy'))
  })

  it('mcpproxy, stdio: the typed command, MCPENV_ variables, no INSECURE', () => {
    start('mcpproxy', 'gw-stdio')
    setting(0, 'envvar:MCP_TRANSPORT', 'stdio')
    role(0).credentials.getState().setDraft({ command: ['npx', '-y', '@mcp/server', '/data'] })
    row(0, 'envvar:MCPENV_NEW_HEADER_1', 'envvar:MCPENV_FIGMA_TOKEN', 'f1')
    expect(payload()).toEqual(fixture('mcpproxy-stdio'))
  })

  it('mcpproxy, remote: a leftover command draft is not sent', () => {
    start('mcpproxy', 'gw-custom')
    role(0).credentials.getState().setDraft({ command: ['npx', 'x'] })
    expect(payload().roles[0].command).toEqual([])
  })

  it('mcpproxy, Secrets Manager: settings stay values, not references (Q37)', () => {
    start('mcpproxy', 'gw-vault')
    store().setRoleMethod(role(0).id, CONNECTION_METHODS.SECRETS_MANAGER)
    store().setRoleProvider(role(0).id, SOURCES.VAULT_KV1)
    typeKey(0, 'envvar:REMOTE_URL', 'secret/mcp:URL')
    typeKey(0, 'envvar:HEADER_Authorization', 'secret/mcp:TOKEN')
    const cljs = fixture('mcpproxy-vault')
    const secret = {
      ...cljs.roles[0].secret,
      'envvar:MCP_TRANSPORT': btoa('streamable-http'),
      'envvar:MCP_AUTH': btoa('static'),
    }
    expect(payload()).toEqual({ ...cljs, roles: [{ ...cljs.roles[0], secret }] })
  })

  it('mcpproxy, OAuth: the token stays manual and the flow id rides on the role', () => {
    start('mcpproxy', 'gw-oauth')
    store().setRoleMethod(role(0).id, CONNECTION_METHODS.SECRETS_MANAGER)
    setting(0, 'envvar:REMOTE_URL', 'https://mcp.linear.app/mcp')
    setting(0, 'envvar:HEADER_AUTHORIZATION', 'Bearer t')
    role(0).credentials.getState().setMcpOAuthFlowId('flow-1')
    store().setRoleProvider(role(0).id, SOURCES.AWS_SECRETS_MANAGER)
    const [sent] = payload().roles
    expect(sent.mcp_oauth_flow_id).toBe('flow-1')
    expect(sent.secret['envvar:HEADER_AUTHORIZATION']).toBe(btoa('Bearer t'))
    expect(sent.secret['envvar:MCP_AUTH']).toBe(btoa('static'))
  })

  it('an OAuth login is staged only for the server it was issued for (Q38)', () => {
    start('mcpproxy', 'gw-oauth')
    const credentials = role(0).credentials
    setting(0, 'envvar:REMOTE_URL', 'https://b.example/mcp')
    expect(credentials.getState().adoptMcpOAuth('https://a.example/mcp', 'Bearer a', 'flow-a')).toBe(false)
    expect(payload().roles[0].secret).not.toHaveProperty('envvar:HEADER_AUTHORIZATION')
    expect(payload().roles[0]).not.toHaveProperty('mcp_oauth_flow_id')

    expect(credentials.getState().adoptMcpOAuth(' https://b.example/mcp ', 'Bearer b', 'flow-b')).toBe(true)
    expect(payload().roles[0].secret['envvar:HEADER_AUTHORIZATION']).toBe(btoa('Bearer b'))
    expect(payload().roles[0].mcp_oauth_flow_id).toBe('flow-b')
  })

  it.each([
    ['the admin edits the URL', (s) => s.replaceSecret('envvar:REMOTE_URL', btoa('https://evil.example/mcp'))],
    ['the admin picks a catalog server', (s) => s.setFormValue('envvar:REMOTE_URL', 'https://mcp.linear.app/mcp')],
    ['the transport turns stdio', (s) => s.cancelSecretChange('envvar:REMOTE_URL')],
  ])('drops the OAuth login when %s (Q38)', (_, change) => {
    start('mcpproxy', 'gw-oauth')
    const credentials = role(0).credentials
    setting(0, 'envvar:REMOTE_URL', 'https://a.example/mcp')
    credentials.getState().adoptMcpOAuth('https://a.example/mcp', 'Bearer a', 'flow-a')
    change(credentials.getState())
    const [sent] = payload().roles
    expect(sent.secret).not.toHaveProperty('envvar:HEADER_AUTHORIZATION')
    expect(sent).not.toHaveProperty('mcp_oauth_flow_id')
  })

  it('keeps a static Authorization header when the URL changes', () => {
    start('mcpproxy', 'gw-custom')
    setting(0, 'envvar:REMOTE_URL', 'https://a.example/mcp')
    typeKey(0, 'envvar:HEADER_Authorization', 'tok-1')
    setting(0, 'envvar:REMOTE_URL', 'https://b.example/mcp')
    expect(payload().roles[0].secret['envvar:HEADER_Authorization']).toBe(btoa('tok-1'))
  })

  it.each([
    [null, 'static'],
    [{ auth: 'oauth' }, 'oauth'],
    [{ auth: 'none' }, 'none'],
    [{ auth: 'static' }, 'static'],
  ])('a picked server %j starts on %s', (entry, mode) => {
    expect(defaultAuthMode(entry)).toBe(mode)
  })

  it('keeps the picked provider, server and auth mode across Back and Next', () => {
    start('mcpproxy', 'gw-back')
    const credentials = role(0).credentials
    credentials.getState().setFormState('mcpServer', 'context7')
    credentials.getState().setFormState('mcpAuthMode', 'oauth')
    typeKey(0, 'envvar:HEADER_CONTEXT7_API_KEY', 'c7')
    store().setStep('agent')
    store().goToRoles()
    expect(role(0).credentials).toBe(credentials)
    expect(credentials.getState().formState).toEqual({ mcpServer: 'context7', mcpAuthMode: 'oauth' })
    expect(secretOf(credentials)['envvar:HEADER_CONTEXT7_API_KEY']).toBe(btoa('c7'))
  })

  it('warns about an MCP OAuth login the gateway did not attach', async () => {
    start('mcpproxy', 'gw-oauth')
    useResourceSetupStore.setState({ roleOptions: 'ready' })
    resourcesService.create.mockResolvedValueOnce({
      data: { mcp_oauth_warnings: [{ name: 'gw-oauth-role', warning: 'flow expired' }] },
    })
    await store().submit(connections)
    expect(showSnackbar).toHaveBeenCalledWith({
      level: 'warning',
      text: 'MCP OAuth login not attached to gw-oauth-role',
      description: 'flow expired',
    })
    expect(store().step).toBe('success')
  })
})

// Fixtures recorded from the CLJS wizard on origin/main for the same input.
describe('POST /resources matches the CLJS wizard: BigQuery', () => {
  const agentId = '618bd713-4223-5529-88b3-39540114f32f'
  const SA = '{"type":"service_account","client_email":"admin@p1.iam.gserviceaccount.com"}'
  const OAUTH = '{"client_id":"x.apps.googleusercontent.com","client_secret":"s"}'
  const start = (name) => {
    begin('bigquery', { name, agentId, profile: null })
    store().setRoleName(role(0).id, `${name}-role`)
  }

  it('Manual: the key file and the project', () => {
    start('bq-manual')
    type(0, 'GOOGLE_APPLICATION_CREDENTIALS', SA)
    type(0, 'CLOUDSDK_CORE_PROJECT', 'p1')
    expect(payload()).toEqual(fixture('bigquery-manual'))
  })

  it('IAM Federation, gcp_iam: the project and the key as the fallback secret', () => {
    start('bq-fed-iam')
    federate(0, { json: SA, project: 'p1' })
    expect(payload()).toEqual(fixture('bigquery-federation-iam'))
  })

  it('IAM Federation, gcp_oauth: the project only, never the OAuth client', () => {
    start('bq-fed-oauth')
    federate(0, { oauth: true, json: OAUTH, project: 'p2' })
    expect(payload()).toEqual(fixture('bigquery-federation-oauth'))
  })

  it('IAM Federation over typed values: the federation project wins, a typed key stays', () => {
    start('bq-then-oauth')
    type(0, 'GOOGLE_APPLICATION_CREDENTIALS', SA)
    type(0, 'CLOUDSDK_CORE_PROJECT', 'p1')
    federate(0, { oauth: true, json: OAUTH, project: 'p3' })
    expect(payload()).toEqual(fixture('bigquery-manual-then-oauth'))
  })

  describe('gcp_iam over earlier input', () => {
    const TYPED = '{"type":"service_account","client_email":"typed@p.iam.gserviceaccount.com","private_key":"k1"}'
    const FED = '{"type":"service_account","client_email":"fed@p.iam.gserviceaccount.com","private_key":"k2"}'
    const startRecorded = (variant) => {
      const name = `qa916-bq-pay-${variant}`
      begin('bigquery', { name, agentId: '43bb5bee-844a-50f7-b9c4-bc68f3e4d862', profile: null })
      store().setRoleName(role(0).id, `${name}-role`)
    }

    it('the federation key replaces a typed key', () => {
      startRecorded('manual-then-iam')
      type(0, 'GOOGLE_APPLICATION_CREDENTIALS', TYPED)
      type(0, 'CLOUDSDK_CORE_PROJECT', 'p1')
      federate(0, { json: FED, project: 'p-fed' })
      expect(payload()).toEqual(fixture('bigquery-manual-then-iam'))
    })

    it('manual values replace Secrets Manager references', () => {
      startRecorded('sm-then-iam')
      store().setRoleMethod(role(0).id, CONNECTION_METHODS.SECRETS_MANAGER)
      type(0, 'GOOGLE_APPLICATION_CREDENTIALS', TYPED)
      type(0, 'CLOUDSDK_CORE_PROJECT', 'secret/bq:project')
      federate(0, { json: FED, project: 'p-fed' })
      expect(payload()).toEqual(fixture('bigquery-sm-then-iam'))
    })

    it('fallback "static": the same POST and PUT', async () => {
      startRecorded('fed-static')
      federate(0, { json: FED, project: 'p-fed' })
      role(0).credentials.getState().setFederationForm({ fallback_policy: 'static' })
      expect(payload()).toEqual(fixture('bigquery-federation-static'))

      useResourceSetupStore.setState({ roleOptions: 'ready' })
      resourcesService.create.mockResolvedValueOnce({ data: {} })
      federationService.save.mockResolvedValueOnce({ data: {} })
      await store().submit(connections)
      // CLJS also sends `enabled`; the gateway ignores it.
      const { enabled: _ignored, ...put } = fixture('bigquery-federation-static.put')
      expect(federationService.save).toHaveBeenCalledWith('qa916-bq-pay-fed-static-role', put)
    })
  })

  it('gives each federation role its own form (Q20)', () => {
    start('bq-two')
    store().addRole()
    federate(0, { json: SA, project: 'p1' })
    federate(1, { oauth: true, json: OAUTH, project: 'p2' })
    const [first, second] = payload().roles
    expect(decoded(first.secret)).toEqual({
      'envvar:CLOUDSDK_CORE_PROJECT': 'p1',
      'filesystem:GOOGLE_APPLICATION_CREDENTIALS': SA,
    })
    expect(decoded(second.secret)).toEqual({ 'envvar:CLOUDSDK_CORE_PROJECT': 'p2' })
    expect(role(0).credentials.getState().federation.form.builtin_provider).toBe('gcp_iam')
  })
})

describe('BigQuery create: federation save and rollback', () => {
  const SA = '{"type":"service_account"}'
  const OAUTH = '{"client_id":"x"}'
  const calls = []

  // Three roles: two federated, one Manual.
  function threeRoles() {
    begin('bigquery', { name: 'bq', agentId: 'a1', profile: null })
    store().addRole()
    store().addRole()
    ;['bq-a', 'bq-b', 'bq-c'].forEach((name, i) => store().setRoleName(role(i).id, name))
    federate(0, { json: SA, project: 'p1' })
    federate(1, { oauth: true, json: OAUTH, project: 'p2' })
    type(2, 'CLOUDSDK_CORE_PROJECT', 'p3')
    useResourceSetupStore.setState({ roleOptions: 'ready' })
  }

  beforeEach(() => {
    calls.length = 0
    resourcesService.create.mockImplementation(async () => {
      calls.push('POST /resources')
      return { data: {} }
    })
    federationService.save.mockImplementation(async (name) => {
      calls.push(`PUT ${name}`)
      return { data: {} }
    })
    connectionsService.deleteConnection.mockImplementation(async (name) => {
      calls.push(`DELETE /connections/${name}`)
    })
    resourcesService.remove.mockImplementation(async (name) => {
      calls.push(`DELETE /resources/${name}`)
    })
  })

  it('saves one federation config per federation role, then finishes', async () => {
    threeRoles()
    await store().submit(connections)
    expect(calls).toEqual(['POST /resources', 'PUT bq-a', 'PUT bq-b'])
    expect(federationService.save).toHaveBeenCalledWith('bq-a', {
      hook_source: 'builtin',
      builtin_provider: 'gcp_iam',
      admin_credentials_json: SA,
      extra_config: { project_id: 'p1' },
      identity_source_attribute: '$.user.email',
      identity_target_template: '{user.email}',
      fallback_policy: 'deny',
      token_ttl_seconds: 3600,
    })
    expect(federationService.save.mock.calls[1][1]).toMatchObject({
      builtin_provider: 'gcp_oauth',
      admin_credentials_json: OAUTH,
      extra_config: { project_id: 'p2' },
    })
    expect(showSnackbar).toHaveBeenCalledWith({ level: 'success', text: 'Resource created successfully!' })
    expect(store().step).toBe('success')
  })

  it('a failed save deletes every role, then the resource, and names the role', async () => {
    threeRoles()
    federationService.save.mockImplementation(async (name) => {
      calls.push(`PUT ${name}`)
      if (name === 'bq-b') throw { response: { data: { message: 'unsupported placeholder {user.handle}' } } }
      return { data: {} }
    })
    await store().submit(connections)
    expect(calls.slice(0, 3)).toEqual(['POST /resources', 'PUT bq-a', 'PUT bq-b'])
    expect(calls.slice(3, 6).sort()).toEqual([
      'DELETE /connections/bq-a',
      'DELETE /connections/bq-b',
      'DELETE /connections/bq-c',
    ])
    expect(calls[6]).toBe('DELETE /resources/bq')
    expect(showSnackbar).toHaveBeenCalledTimes(1)
    expect(showSnackbar).toHaveBeenCalledWith({
      level: 'error',
      text: 'IAM Federation setup failed for bq-b. The resource was not created.',
      description: 'unsupported placeholder {user.handle}',
    })
    expect(store()).toMatchObject({ step: 'roles', submitting: false })
  })

  it('names what the rollback could not delete', async () => {
    threeRoles()
    federationService.save.mockRejectedValue(new Error('boom'))
    connectionsService.deleteConnection.mockImplementation(async (name) => {
      calls.push(`DELETE /connections/${name}`)
      if (name === 'bq-c') throw new Error('busy')
    })
    await store().submit(connections)
    expect(resourcesService.remove).not.toHaveBeenCalled()
    expect(showSnackbar).toHaveBeenCalledWith({
      level: 'error',
      text: 'IAM Federation setup failed for bq-a, bq-b. Could not delete bq-c, bq: delete them before you try again.',
      description: 'boom',
    })
  })

  it('rolls back after the admin left the wizard, without a toast', async () => {
    threeRoles()
    const pending = []
    federationService.save.mockImplementation(() => new Promise((_, reject) => pending.push(reject)))
    const submitting = store().submit(connections)
    await vi.waitFor(() => expect(pending).toHaveLength(2))
    store().start(setupConfigFor('postgres', connections))
    pending.forEach((reject) => reject(new Error('late')))
    await submitting
    expect(resourcesService.remove).toHaveBeenCalledWith('bq')
    expect(showSnackbar).not.toHaveBeenCalled()
  })

  it('a resource with no federation role sends no PUT', async () => {
    begin('bigquery', { name: 'bq-plain', agentId: 'a1', profile: null })
    useResourceSetupStore.setState({ roleOptions: 'ready' })
    await store().submit(connections)
    expect(federationService.save).not.toHaveBeenCalled()
    expect(store().step).toBe('success')
  })
})

describe('wizard store', () => {
  it('starts clean: no role, agent or key of an earlier run (Q14, Q15)', async () => {
    agentsService.create.mockResolvedValue({ data: { token: 'old-key' } })
    agentsService.list.mockResolvedValue({ data: [{ id: 'a1', name: 'agent-1' }] })
    begin('postgres', { name: 'first', agentId: null })
    store().setAgentMode('create')
    store().setNewAgentName('agent-1')
    await store().createAgent()
    expect(store().newAgent).toEqual({ name: 'agent-1', status: 'ready', token: 'old-key', lookup: 'done' })
    expect(store().agentId).toBe('a1')

    store().start(setupConfigFor('mysql', connections))
    expect(store()).toMatchObject({
      step: 'name',
      name: '',
      agentId: null,
      agentMode: 'select',
      newAgent: { name: '', status: 'idle', token: null, lookup: 'idle' },
      roles: [],
      setup: { subtype: 'mysql' },
    })
  })

  it('keeps a default role name in step with the resource name (Q21)', () => {
    begin('postgres', { name: 'pg', agentId: 'a1' })
    store().addRole()
    store().setRoleName(role(1).id, 'custom')
    store().setName('pg prod')
    expect(store().name).toBe('pg-prod')
    expect(role(0).name).toBe(`pg-prod-role-${role(0).suffix}`)
    expect(role(1).name).toBe('custom')
  })

  it('moves typed values to the new source on a method switch, as CLJS does', () => {
    begin('postgres', { name: 'pg', agentId: 'a1' })
    type(0, 'HOST', 'db')
    type(0, 'USER', 'app')
    type(0, 'PASS', 'secret')
    store().setRoleMethod(role(0).id, CONNECTION_METHODS.SECRETS_MANAGER)
    expect(secretOf(role(0).credentials)).toEqual({
      'envvar:HOST': btoa('_vaultkv1:db'),
      'envvar:USER': btoa('_vaultkv1:app'),
      'envvar:PASS': btoa('_vaultkv1:secret'),
    })

    role(0).credentials.getState().setFieldSource('envvar:HOST', SOURCES.MANUAL)
    store().setRoleProvider(role(0).id, SOURCES.AWS_SECRETS_MANAGER)
    expect(secretOf(role(0).credentials)['envvar:HOST']).toBe(btoa('_aws:db'))

    store().setRoleMethod(role(0).id, CONNECTION_METHODS.AWS_IAM)
    awsIamPass(0)
    expect(secretOf(role(0).credentials)).toEqual({
      'envvar:HOST': btoa('db'),
      'envvar:USER': btoa('_aws_iam_rds:app'),
      'envvar:PASS': btoa('_aws_iam_rds:authtoken'),
    })

    // Leaving AWS IAM brings back the PASS the admin typed, not `authtoken`.
    store().setRoleMethod(role(0).id, CONNECTION_METHODS.MANUAL)
    expect(secretOf(role(0).credentials)).toEqual({
      'envvar:HOST': btoa('db'),
      'envvar:USER': btoa('app'),
      'envvar:PASS': btoa('secret'),
    })
  })

  it('drops the forced PASS when the admin never typed one', () => {
    begin('mysql', { name: 'my', agentId: 'a1' })
    store().setRoleMethod(role(0).id, CONNECTION_METHODS.AWS_IAM)
    awsIamPass(0)
    store().setRoleMethod(role(0).id, CONNECTION_METHODS.MANUAL)
    expect(secretOf(role(0).credentials)).toEqual({})
  })

  it('leaves a cleared field out of the secret', () => {
    begin('postgres', { name: 'pg', agentId: 'a1' })
    type(0, 'HOST', 'db')
    type(0, 'HOST', '')
    expect(secretOf(role(0).credentials)).toEqual({})
  })

  it('checks the name: 404 moves on, 200 stays with an error', async () => {
    begin('postgres', { name: 'taken', agentId: 'a1' })
    store().setStep('name')
    resourcesService.get.mockResolvedValueOnce({ data: { name: 'taken' } })
    await store().checkName()
    expect(store().step).toBe('name')
    expect(showSnackbar).toHaveBeenCalledWith(expect.objectContaining({ level: 'error' }))

    resourcesService.get.mockRejectedValueOnce({ response: { status: 404 } })
    await store().checkName()
    expect(store().step).toBe('agent')
  })

  it('drops a name check whose name changed while it ran', async () => {
    begin('postgres', { name: 'old', agentId: 'a1' })
    store().setStep('name')
    let answer
    resourcesService.get.mockReturnValueOnce(new Promise((_, reject) => { answer = reject }))
    const check = store().checkName()
    store().setName('new')
    answer({ response: { status: 404 } })
    await check
    expect(store()).toMatchObject({ step: 'name', checkingName: false })
  })

  it('keeps an older agent list from replacing a newer one', async () => {
    begin('postgres', { name: 'pg', agentId: null })
    let answerOld
    agentsService.list
      .mockReturnValueOnce(new Promise((resolve) => { answerOld = resolve }))
      .mockResolvedValueOnce({ data: [{ id: 'a2', name: 'new' }] })
    const old = store().loadAgents()
    await store().loadAgents()
    answerOld({ data: [{ id: 'a1', name: 'old' }] })
    await old
    expect(store().agents).toEqual([{ id: 'a2', name: 'new' }])
  })

  it('keeps the new agent key and retries the id lookup', async () => {
    begin('postgres', { name: 'pg', agentId: null })
    agentsService.create.mockResolvedValue({ data: { token: 'key' } })
    agentsService.list.mockResolvedValueOnce({ data: [] })
    store().setNewAgentName('agent-1')
    await store().createAgent()
    expect(store().newAgent).toMatchObject({ status: 'ready', token: 'key', lookup: 'error' })
    expect(store().agentId).toBeNull()

    agentsService.list.mockResolvedValueOnce({ data: [{ id: 'a1', name: 'agent-1' }] })
    await store().findNewAgent()
    expect(store().newAgent).toMatchObject({ token: 'key', lookup: 'done' })
    expect(store().agentId).toBe('a1')
    expect(agentsService.create).toHaveBeenCalledTimes(1)
  })

  it('drops a create still in flight when the admin leaves the agent step', async () => {
    begin('postgres', { name: 'pg', agentId: null })
    store().setStep('agent')
    let answer
    agentsService.create.mockReturnValueOnce(new Promise((resolve) => { answer = resolve }))
    store().setNewAgentName('agent-1')
    const creating = store().createAgent()
    store().leaveAgentStep()
    answer({ data: { token: 'old-key' } })
    await creating
    expect(store()).toMatchObject({ agentId: null, newAgent: { status: 'idle', token: null } })
    expect(agentsService.list).not.toHaveBeenCalled()
  })

  it('keeps the newest of two id lookups', async () => {
    begin('postgres', { name: 'pg', agentId: null })
    useResourceSetupStore.setState({ newAgent: { name: 'agent-1', status: 'ready', token: 'key', lookup: 'error' } })
    let answerOld
    agentsService.list
      .mockReturnValueOnce(new Promise((resolve) => { answerOld = resolve }))
      .mockResolvedValueOnce({ data: [{ id: 'a1', name: 'agent-1' }] })
    const old = store().findNewAgent()
    await store().findNewAgent()
    answerOld({ data: [] })
    await old
    expect(store()).toMatchObject({ agentId: 'a1', newAgent: { lookup: 'done' } })
  })

  it('keeps the newest of two role-option loads', async () => {
    const { protectionProfilesService } = await import('@/services/protectionProfiles')
    begin('postgres', { name: 'pg', agentId: 'a1' })
    let failOld
    protectionProfilesService.get
      .mockReturnValueOnce(new Promise((_, reject) => { failOld = reject }))
      .mockResolvedValueOnce(PROFILE)
    const old = store().loadRoleOptions()
    await store().loadRoleOptions()
    failOld(new Error('late'))
    await old
    expect(store()).toMatchObject({ roleOptions: 'ready', profile: PROFILE })
  })

  it('puts a new attribute on the role at once, and takes it off when the create fails', async () => {
    const { attributesService } = await import('@/services/attributes')
    begin('postgres', { name: 'pg', agentId: 'a1' })
    let fail
    attributesService.create.mockReturnValueOnce(new Promise((_, reject) => { fail = reject }))
    const created = store().createAttribute(role(0).id, 'pii')
    expect(role(0).attributes).toEqual(['pii'])
    expect(payload().roles[0].attributes).toContain('pii')

    fail(new Error('boom'))
    await created
    expect(role(0).attributes).toEqual([])
    expect(showSnackbar).toHaveBeenCalledWith(expect.objectContaining({ level: 'error' }))
  })

  it('saves only once the protection profile is known', async () => {
    const { protectionProfilesService } = await import('@/services/protectionProfiles')
    begin('postgres', { name: 'pg', agentId: 'a1' })
    protectionProfilesService.get.mockRejectedValueOnce(new Error('down'))
    await store().loadRoleOptions()
    expect(store().roleOptions).toBe('error')
    await store().submit(connections)
    expect(resourcesService.create).not.toHaveBeenCalled()

    protectionProfilesService.get.mockResolvedValueOnce(PROFILE)
    await store().loadRoleOptions()
    expect(store().roleOptions).toBe('ready')
    resourcesService.create.mockResolvedValueOnce({})
    await store().submit(connections)
    expect(resourcesService.create.mock.calls[0][0].roles[0].attributes).toContain(PROFILE.attribute_name)
  })

  it('saves only once the attributes are known', async () => {
    const { attributesService } = await import('@/services/attributes')
    begin('postgres', { name: 'pg', agentId: 'a1' })
    attributesService.listAll.mockRejectedValueOnce(new Error('down'))
    await store().loadRoleOptions()
    expect(store().roleOptions).toBe('error')
    await store().submit(connections)
    expect(resourcesService.create).not.toHaveBeenCalled()
  })

  it('drops the selected agent when the admin creates a new one', () => {
    begin('postgres', { name: 'pg', agentId: 'a1' })
    store().setAgentMode('create')
    expect(store().agentId).toBeNull()
  })

  it('drops a save that answers after a new run started', async () => {
    begin('postgres', { name: 'pg', agentId: 'a1' })
    await store().loadRoleOptions()
    let answer
    resourcesService.create.mockReturnValueOnce(new Promise((resolve) => { answer = resolve }))
    const saving = store().submit(connections)
    store().start(setupConfigFor('mysql', connections))
    answer({})
    await saving
    expect(store()).toMatchObject({ step: 'name', submitting: false, createdRoles: [] })
    expect(showSnackbar).not.toHaveBeenCalled()
  })

  it('drops activation data of an earlier run', async () => {
    const { guardrailsService } = await import('@/services/guardrails')
    const { dataMaskingService } = await import('@/services/dataMasking')
    const { aiSessionAnalyzerService } = await import('@/services/aiSessionAnalyzer')
    begin('postgres', { name: 'pg', agentId: 'a1' })
    let answer
    guardrailsService.list.mockReturnValueOnce(new Promise((resolve) => { answer = resolve }))
    dataMaskingService.list.mockResolvedValueOnce({ data: [] })
    aiSessionAnalyzerService.listRules.mockResolvedValueOnce({ data: { data: [] } })
    aiSessionAnalyzerService.getProvider.mockResolvedValueOnce({ data: {} })
    const loading = store().loadActivation()
    store().start(setupConfigFor('mysql', connections))
    answer({ data: [] })
    await loading
    expect(store().activation).toBeNull()
  })
})

describe('activation cards', () => {
  const lists = { guardrails: [], masking: [], 'ai-analyzer': [] }
  const gates = { guardrails: true, masking: true, 'ai-analyzer': true }
  const roles = { connectionIds: ['c1'], connectionNames: ['pg-ro'] }
  const cards = (over = {}) => buildCards({ subtype: 'postgres', free: false, gates, lists, ...roles, ...over })

  it('orders Guardrails, Live Data Masking, AI Session Analyzer', () => {
    expect(cards().map((c) => c.feature)).toEqual(['guardrails', 'masking', 'ai-analyzer'])
  })

  it('marks a feature already enabled for a created role', () => {
    const [guardrails] = cards({ lists: { ...lists, guardrails: [{ name: 'x', connection_ids: ['c1'] }] } })
    expect(guardrails).toMatchObject({ enabled: true, cta: { kind: 'list' } })
  })

  it('sends a feature without its provider to the list page', () => {
    const [, masking] = cards({ gates: { ...gates, masking: false } })
    expect(masking.cta.kind).toBe('list')
  })

  it('locks a configured feature on the free plan', () => {
    const [guardrails] = cards({ free: true, lists: { ...lists, guardrails: [{ name: 'other' }] } })
    expect(guardrails).toMatchObject({ locked: true, cta: { kind: 'talk-to-sales' } })
  })

  it('offers the next template not configured yet on enterprise', () => {
    const [guardrails] = cards({ lists: { ...lists, guardrails: [{ name: 'pg-block-unsafe-write' }] } })
    expect(guardrails.cta).toMatchObject({ kind: 'create-with-template', templateId: 'pg-block-destructive-ddl' })
  })

  it('falls back to the create page when every template is configured', () => {
    const configured = ['pg-block-unsafe-write', 'pg-block-destructive-ddl', 'pg-mask-pgcrypto-output']
    const [guardrails] = cards({ lists: { ...lists, guardrails: configured.map((name) => ({ name })) } })
    expect(guardrails.cta.kind).toBe('create')
  })

  it('links the template with role ids, or names for the analyzer', () => {
    const [guardrails, , analyzer] = cards()
    expect(ctaPath(guardrails, roles)).toBe('/guardrails/new?template=pg-block-unsafe-write&connections=c1')
    expect(ctaPath(analyzer, roles)).toBe(
      '/features/ai-session-analyzer/rules/new?template=postgres-block-destructive-ddl&connections=pg-ro',
    )
  })
})
