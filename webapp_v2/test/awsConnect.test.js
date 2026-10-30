import { beforeEach, describe, expect, it, vi } from 'vitest'

const awsConnectService = {
  saveCredentials: vi.fn(),
  listAccounts: vi.fn(),
  describeDbInstances: vi.fn(),
  resetRootPasswords: vi.fn(),
  listJobs: vi.fn(),
  createJob: vi.fn(),
}
const showSnackbar = vi.fn()

vi.mock('@/services/awsConnect', () => ({ awsConnectService }))
vi.mock('@/utils/snackbar', () => ({ showSnackbar }))
vi.mock('@/stores/useAuthStore', () => ({ useAuthStore: { subscribe: () => () => {} } }))

const { errorMessage, isRunning, limitReached, toJobRows } = await import('@/pages/Integrations/AwsConnect/utils')
const { useAwsJobsStore } = await import('@/pages/Integrations/AwsConnect/store')
const { useAwsConnectSetupStore } = await import('@/pages/Integrations/AwsConnect/Setup/store')
const wizard = await import('@/pages/Integrations/AwsConnect/Setup/wizard')

const apiError = (message, status = 400) => Object.assign(new Error('Request failed'), { response: { status, data: { message } } })

function deferred() {
  let resolve
  const promise = new Promise((r) => {
    resolve = r
  })
  return { promise, resolve }
}

const ACCOUNTS = [
  { account_id: '111', name: 'prod', status: 'ACTIVE' },
  { account_id: '222', name: 'dev', status: 'ACTIVE' },
  { account_id: '333', name: 'denied', status: 'ACTIVE' },
]
const INSTANCES = [
  { account_id: '111', name: 'pg-a', arn: 'arn:aws:rds:us-east-1:111:db:pg-a', engine: 'postgres', status: 'available', connection_resources: [] },
  { account_id: '111', name: 'pg-b', arn: 'arn:aws:rds:us-east-1:111:db:pg-b', engine: 'postgres', status: 'available', connection_resources: ['pg-b-ro'] },
  { account_id: '222', name: 'my-a', arn: 'arn:aws:rds:us-east-1:222:db:my-a', engine: 'mysql', status: 'stopped', connection_resources: null },
  { account_id: '333', name: '', arn: '', error: 'AccessDenied: not authorized' },
]

beforeEach(() => {
  vi.clearAllMocks()
  useAwsJobsStore.getState().reset()
  useAwsConnectSetupStore.getState().reset()
})

describe('jobs', () => {
  const job = (id, createdAt, overrides = {}) => ({
    id,
    created_at: createdAt,
    completed_at: '2026-10-30T10:05:00Z',
    spec: { db_arn: `arn:aws:rds:us-east-1:111:db:${id}`, db_engine: 'postgres' },
    status: { phase: 'completed', message: 'done', result: [] },
    ...overrides,
  })

  it('names a job after its ARN and a role after its user, newest first', () => {
    const rows = toJobRows([
      job('older', '2026-10-30T09:00:00Z'),
      job('newer', '2026-10-30T10:00:00Z', {
        status: { phase: 'completed', message: '', result: [{ user_role: 'hoop_ro', status: 'completed', message: '' }] },
      }),
    ])
    expect(rows.map((r) => r.name)).toEqual(['newer', 'older'])
    expect(rows[0].children).toEqual([expect.objectContaining({ name: 'newer-ro', error: null })])
  })

  it('carries the message of a failed job and of a failed role, never the raw object (Q30)', () => {
    const [row] = toJobRows([
      job('pg', '2026-10-30T10:00:00Z', {
        status: { phase: 'failed', message: 'agent offline', result: [{ user_role: 'hoop_rw', status: 'failed', message: 'permission denied' }] },
      }),
    ])
    expect(row.error).toBe('agent offline')
    expect(row.children[0].error).toBe('permission denied')
  })

  it('renders a job without db_arn or user_role (Q35)', () => {
    const [row] = toJobRows([
      { id: 'j1', created_at: '2026-10-30T10:00:00Z', completed_at: null, spec: { db_arn: '' }, status: { phase: 'running', result: [{ user_role: '', status: 'running' }] } },
      { id: 'j2', created_at: '2026-10-30T09:00:00Z', completed_at: null, spec: {}, status: null },
    ])
    expect(row.name).toBe('')
    expect(row.children[0].name).toBe('')
  })

  it('runs while completed_at is empty (Q28)', () => {
    expect(isRunning({ completed_at: null })).toBe(true)
    expect(isRunning({ completed_at: '2026-10-30T10:05:00Z' })).toBe(false)
  })

  it('reaches the free limit with one job that did not fail', () => {
    expect(limitReached([{ status: { phase: 'failed' } }])).toBe(false)
    expect(limitReached([{ status: { phase: 'failed' } }, { status: { phase: 'running' } }])).toBe(true)
  })

  it('reads an empty list from items: null', async () => {
    awsConnectService.listJobs.mockResolvedValue({ data: { items: null } })
    await useAwsJobsStore.getState().fetchJobs()
    expect(useAwsJobsStore.getState()).toMatchObject({ jobs: [], loaded: true, error: null })
  })

  it('polls every 5 s while a job runs and stops when none runs (Q28)', async () => {
    vi.useFakeTimers()
    try {
      awsConnectService.listJobs.mockResolvedValue({ data: { items: [job('a', 'x', { completed_at: null })] } })
      await useAwsJobsStore.getState().startPolling()
      expect(awsConnectService.listJobs).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(5000)
      expect(awsConnectService.listJobs).toHaveBeenCalledTimes(2)

      awsConnectService.listJobs.mockResolvedValue({ data: { items: [job('a', 'x')] } })
      await vi.advanceTimersByTimeAsync(5000)
      expect(awsConnectService.listJobs).toHaveBeenCalledTimes(3)
      await vi.advanceTimersByTimeAsync(20000)
      expect(awsConnectService.listJobs).toHaveBeenCalledTimes(3)
    } finally {
      vi.useRealTimers()
    }
  })

  it('stops polling when the page leaves', async () => {
    vi.useFakeTimers()
    try {
      awsConnectService.listJobs.mockResolvedValue({ data: { items: [job('a', 'x', { completed_at: null })] } })
      await useAwsJobsStore.getState().startPolling()
      useAwsJobsStore.getState().stopPolling()
      await vi.advanceTimersByTimeAsync(20000)
      expect(awsConnectService.listJobs).toHaveBeenCalledTimes(1)
    } finally {
      vi.useRealTimers()
    }
  })

  it('does not poll outside the jobs page', async () => {
    vi.useFakeTimers()
    try {
      awsConnectService.listJobs.mockResolvedValue({ data: { items: [job('a', 'x', { completed_at: null })] } })
      await useAwsJobsStore.getState().fetchJobs()
      await vi.advanceTimersByTimeAsync(20000)
      expect(awsConnectService.listJobs).toHaveBeenCalledTimes(1)
    } finally {
      vi.useRealTimers()
    }
  })

  it('keeps the newest answer when an older one arrives last', async () => {
    const first = deferred()
    awsConnectService.listJobs.mockReturnValueOnce(first.promise).mockResolvedValueOnce({ data: { items: [job('new', 'x')] } })
    const slow = useAwsJobsStore.getState().fetchJobs()
    await useAwsJobsStore.getState().fetchJobs()
    first.resolve({ data: { items: [] } })
    await slow
    expect(useAwsJobsStore.getState().jobs.map((j) => j.id)).toEqual(['new'])
  })
})

describe('errorMessage', () => {
  it('reads the gateway message, else the fallback (Q30)', () => {
    expect(errorMessage(apiError('bad region'), 'x')).toBe('bad region')
    expect(errorMessage(new Error('Network Error'), 'Failed')).toBe('Failed')
  })
})

describe('wizard helpers', () => {
  it('sends only the region for the gateway instance profile', () => {
    const credentials = { accessKeyId: 'AKIA', secretAccessKey: 's', sessionToken: '', region: 'us-east-1' }
    expect(wizard.credentialsBody(wizard.AUTH_GATEWAY_PROFILE, credentials)).toEqual({ region: 'us-east-1' })
    expect(wizard.credentialsBody(wizard.AUTH_CREDENTIALS, credentials)).toEqual({
      access_key_id: 'AKIA',
      secret_access_key: 's',
      region: 'us-east-1',
      session_token: '',
    })
  })

  it('groups instances by account, skips connected ones and keeps an account error', () => {
    const { tree, connected } = wizard.buildResourceTree(INSTANCES, ACCOUNTS, true)
    expect(tree.map((a) => [a.id, a.children.map((c) => c.name), a.error])).toEqual([
      ['111', ['pg-a'], null],
      ['222', ['my-a'], null],
      ['333', [], 'AccessDenied: not authorized'],
    ])
    expect(connected.map((i) => i.name)).toEqual(['pg-b'])
  })

  it('keeps connected instances and empty accounts when skip is off', () => {
    const { tree } = wizard.buildResourceTree(INSTANCES.slice(0, 2), ACCOUNTS, false)
    expect(tree.map((a) => [a.id, a.children.length])).toEqual([
      ['111', 2],
      ['222', 0],
      ['333', 0],
    ])
  })

  it('selects an account as its resources, and one resource on the free plan', () => {
    const { tree } = wizard.buildResourceTree(INSTANCES, ACCOUNTS, false)
    const prod = tree[0]
    const all = wizard.toggleResource([], prod, true, false)
    expect(all).toEqual(prod.children.map((c) => c.id))
    expect(wizard.accountChecked(prod, all)).toBe(true)
    expect(wizard.accountChecked(prod, all.slice(0, 1))).toBe('some')
    expect(wizard.toggleResource(all, prod, false, false)).toEqual([])
    expect(wizard.toggleResource(all, tree[1].children[0], true, true)).toEqual([tree[1].children[0].id])
  })

  it('builds the job payload from the review choices', () => {
    const resource = { id: 'arn:pg-a', name: 'pg-a', accountId: '111' }
    const state = {
      agentAssignments: { 'arn:pg-a': 'agent-1' },
      securityGroups: {},
      roleNames: {},
      createConnection: true,
      vaultEnabled: true,
      vaultPath: '',
    }
    expect(wizard.jobPayload(resource, state)).toEqual({
      agent_id: 'agent-1',
      aws: { instance_arn: 'arn:pg-a', default_security_group: null },
      connection_prefix_name: 'pg-a-111-',
      job_steps: ['create-connections', 'send-webhook'],
    })
    expect(
      wizard.jobPayload(resource, {
        ...state,
        securityGroups: { 'arn:pg-a': '10.0.0.0/24' },
        roleNames: { 'arn:pg-a': 'billing' },
        createConnection: false,
        vaultPath: 'dbsecrets/data/',
      }),
    ).toEqual({
      agent_id: 'agent-1',
      aws: { instance_arn: 'arn:pg-a', default_security_group: { ingress_cidr: '10.0.0.0/24' } },
      connection_prefix_name: 'billing-',
      job_steps: ['send-webhook'],
      vault_provider: { secret_id: 'dbsecrets/data/' },
    })
  })
})

describe('setup store', () => {
  const store = () => useAwsConnectSetupStore.getState()

  it('starts on credentials with the keys method, so the key fields show on a direct load (Q29)', () => {
    expect(store()).toMatchObject({ step: 'credentials', authMethod: wizard.AUTH_CREDENTIALS })
  })

  it('saves the credentials, then lists the accounts', async () => {
    awsConnectService.saveCredentials.mockResolvedValue({ status: 204 })
    awsConnectService.listAccounts.mockResolvedValue({ data: { items: ACCOUNTS } })
    store().setCredential('region', 'us-east-1')
    await store().saveCredentials()
    expect(awsConnectService.saveCredentials).toHaveBeenCalledWith(expect.objectContaining({ region: 'us-east-1' }))
    expect(store()).toMatchObject({ step: 'accounts', busy: null, accounts: ACCOUNTS })
  })

  it('shows the message of a credentials failure (Q30)', async () => {
    awsConnectService.saveCredentials.mockRejectedValue(apiError('unable to parse request body'))
    await store().saveCredentials()
    expect(store()).toMatchObject({ step: 'credentials', busy: null, credentialsError: 'unable to parse request body' })
  })

  it('keeps a describe failure on the Accounts step (Q31)', async () => {
    store().update({ step: 'accounts', accounts: ACCOUNTS, selectedAccounts: ['111'] })
    awsConnectService.describeDbInstances.mockRejectedValue(apiError('failed to get AWS accounts'))
    await store().describeInstances()
    expect(awsConnectService.describeDbInstances).toHaveBeenCalledWith(['111'])
    expect(store()).toMatchObject({ step: 'accounts', busy: null, accountsError: 'failed to get AWS accounts' })
  })

  it('starts one job per selected resource and says started, not done (Q34)', async () => {
    const { tree } = wizard.buildResourceTree(INSTANCES, ACCOUNTS, true)
    const [pgA, myA] = [tree[0].children[0], tree[1].children[0]]
    store().update({
      tree,
      selectedResources: [pgA.id, myA.id],
      agentAssignments: { [pgA.id]: 'agent-1', [myA.id]: 'agent-1' },
      rootPasswords: { credentials: {} },
    })
    awsConnectService.createJob.mockImplementation(async (body) => {
      if (body.aws.instance_arn === myA.id) throw apiError('agent does not exists')
      return { status: 202, data: { job_id: 'j1' } }
    })
    await store().startJobs()
    expect(awsConnectService.createJob).toHaveBeenCalledTimes(2)
    expect(store().rootPasswords).toBeNull()
    expect(wizard.statusRows(store().jobs).map((r) => [r.name, r.status, r.error])).toEqual([
      ['my-a-222', 'failure', 'agent does not exists'],
      ['pg-a-111', 'success', null],
    ])
  })

  it('drops an answer that arrives after a reset', async () => {
    const pending = deferred()
    awsConnectService.saveCredentials.mockReturnValue(pending.promise)
    const run = store().saveCredentials()
    store().reset()
    pending.resolve({ status: 204 })
    await run
    expect(awsConnectService.listAccounts).not.toHaveBeenCalled()
    expect(store()).toMatchObject({ step: 'credentials', busy: null })
  })
})
