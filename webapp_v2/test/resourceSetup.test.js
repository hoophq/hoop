import { readFileSync } from 'node:fs'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const fixture = (name) =>
  JSON.parse(readFileSync(new URL(`./fixtures/resourceSetup/${name}.json`, import.meta.url), 'utf8'))
const { connections } = fixture('metadata')

const agentsService = { list: vi.fn(), create: vi.fn() }
const resourcesService = { get: vi.fn(), create: vi.fn() }
const showSnackbar = vi.fn()

vi.mock('@/services/agents', () => ({ agentsService }))
vi.mock('@/services/resources', () => ({ resourcesService }))
vi.mock('@/services/attributes', () => ({
  attributesService: { listAll: vi.fn(async () => []), create: vi.fn(async () => ({})) },
}))
vi.mock('@/services/protectionProfiles', () => ({
  protectionProfilesService: { get: vi.fn(async () => ({ profile: null, attribute_name: null })) },
}))
vi.mock('@/services/connections', () => ({ connectionsService: { list: vi.fn() } }))
vi.mock('@/services/guardrails', () => ({ guardrailsService: { list: vi.fn() } }))
vi.mock('@/services/dataMasking', () => ({ dataMaskingService: { list: vi.fn() } }))
vi.mock('@/services/aiSessionAnalyzer', () => ({
  aiSessionAnalyzerService: { listRules: vi.fn(), getProvider: vi.fn() },
}))
vi.mock('@/utils/snackbar', () => ({ showSnackbar }))

const { setupConfigFor, isReactSetup, metadataForSubtype } = await import('@/pages/Resources/Setup/catalog')
const { useResourceSetupStore } = await import('@/pages/Resources/Setup/store')
const { buildCreatePayload } = await import('@/pages/Resources/Setup/payload')
const { secretOf } = await import('@/pages/Resources/Setup/roleCredentials')
const { buildCards, ctaPath } = await import('@/pages/Resources/Setup/activation')
const { jsonCredentialToField } = await import('@/utils/connectionsMetadataMapper')
const { encodeSecretForSource, sourceFromEncodedValue, SOURCES } = await import(
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
    ['bigquery', false],
  ])('%s renders React: %s', (id, react) => {
    expect(isReactSetup(setupConfigFor(id, connections))).toBe(react)
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
