import { beforeEach, describe, expect, it, vi } from 'vitest'

const calls = []
const federationService = {
  get: vi.fn(),
  save: vi.fn(async () => {
    calls.push('PUT federation')
    return { data: { builtin_provider: 'gcp_iam', has_admin_credentials: true, extra_config: { project_id: 'p' } } }
  }),
  remove: vi.fn(async () => {
    calls.push('DELETE federation')
  }),
}
const connectionsService = {
  patchConnection: vi.fn(async (name) => {
    calls.push('PATCH connection')
    return { name, type: 'custom', subtype: 'bigquery', secret: {} }
  }),
}

vi.mock('@/services/federation', () => ({ federationService }))
vi.mock('@/services/connections', () => ({ connectionsService }))
vi.mock('@/services/guardrails', () => ({ guardrailsService: {} }))
vi.mock('@/services/jiraTemplates', () => ({ jiraTemplatesService: {} }))
vi.mock('@/services/attributes', () => ({ attributesService: {} }))
vi.mock('@/services/connectionTags', () => ({ connectionTagsService: {} }))
vi.mock('@/services/userGroups', () => ({ userGroupsService: {} }))
vi.mock('@/services/infrastructure', () => ({ default: {} }))

const { useConfigureRoleStore } = await import('@/pages/Roles/Configure/store')
const { formFromConfig } = await import('@/pages/Roles/Configure/utils/federation')

const CONFIG = {
  builtin_provider: 'gcp_iam',
  has_admin_credentials: true,
  extra_config: { project_id: 'p' },
  identity_source_attribute: '$.user.email',
  identity_target_template: '{user.email}',
  fallback_policy: 'deny',
  token_ttl_seconds: 3600,
}

function seed({ config, selected }) {
  const form = formFromConfig(config)
  useConfigureRoleStore.setState({
    connection: { name: 'bq', type: 'custom', subtype: 'bigquery', secret: {} },
    federation: {
      ...useConfigureRoleStore.getState().federation,
      status: 'ready',
      config,
      form,
      baselineForm: form,
      selected,
    },
  })
}

beforeEach(() => {
  calls.length = 0
  vi.clearAllMocks()
  useConfigureRoleStore.getState().reset()
})

describe('Configure Role save with IAM federation', () => {
  it('removes the federation only after the connection PATCH succeeds', async () => {
    seed({ config: CONFIG, selected: false })
    await useConfigureRoleStore.getState().save()
    expect(calls).toEqual(['PATCH connection', 'DELETE federation'])
    expect(useConfigureRoleStore.getState().federation.config).toBeNull()
  })

  it('keeps the federation when the PATCH fails', async () => {
    seed({ config: CONFIG, selected: false })
    connectionsService.patchConnection.mockRejectedValueOnce(new Error('boom'))
    await expect(useConfigureRoleStore.getState().save()).rejects.toThrow('boom')
    expect(federationService.remove).not.toHaveBeenCalled()
    expect(useConfigureRoleStore.getState().hasPendingChanges()).toBe(true)
  })

  it('writes the federation before the PATCH and says so when the PATCH fails', async () => {
    seed({ config: null, selected: true })
    useConfigureRoleStore.getState().setFederationForm({ admin_credentials_json: '{"type":"service_account"}' })
    connectionsService.patchConnection.mockRejectedValueOnce(new Error('boom'))
    await expect(useConfigureRoleStore.getState().save()).rejects.toThrow(
      'The IAM federation was saved, the other changes were not: boom',
    )
    expect(calls).toEqual(['PUT federation'])
  })

  it('clears a typed credential when the provider changes', () => {
    seed({ config: CONFIG, selected: true })
    useConfigureRoleStore.getState().setFederationForm({ admin_credentials_json: '{"client_id":"x"}' })
    useConfigureRoleStore.getState().setFederationProvider('gcp_oauth')
    expect(useConfigureRoleStore.getState().federation.form.admin_credentials_json).toBe('')
  })
})
