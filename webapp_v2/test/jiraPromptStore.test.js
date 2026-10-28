import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const connectionsService = { getConnection: vi.fn() }
const jiraTemplatesService = {
  getIntegration: vi.fn(),
  get: vi.fn(),
  getAssetFieldConfigs: vi.fn(),
  searchAssetObjects: vi.fn(),
}

vi.mock('@/services/connections', () => ({ connectionsService }))
vi.mock('@/services/jiraTemplates', () => ({ jiraTemplatesService }))
vi.mock('@/stores/useAuthStore', () => ({ useAuthStore: { subscribe: vi.fn() } }))

const { useJiraPromptStore, GATE_STATUS } = await import('@/stores/useJiraPromptStore')

const ok = (data) => Promise.resolve({ data })
const fail = () => Promise.reject(Object.assign(new Error('boom'), { response: { status: 500 } }))

const servers = [
  { id: 'ari:server:1', name: 'db-prod-01' },
  { id: 'ari:server:2', name: 'db-prod-02' },
]

const prompt = (jiraField, fieldType, required = false) => ({
  label: `Label of ${jiraField}`,
  jira_field: jiraField,
  field_type: fieldType,
  required,
})

const cmdb = (jiraField, objectType, { value = '', required = false } = {}) => ({
  label: `Label of ${jiraField}`,
  jira_field: jiraField,
  jira_object_type: objectType,
  jira_object_schema_id: '7',
  value,
  required,
})

// A resource role with a template, an enabled integration, and CMDB values
// answered per object type.
function seed({ prompts = [], cmdbItems = [], fieldconfigs = [], values = {} } = {}) {
  connectionsService.getConnection.mockImplementation(() =>
    ok({ name: 'pg', jira_issue_template_id: 'tpl-1' }),
  )
  jiraTemplatesService.getIntegration.mockImplementation(() => ok({ status: 'enabled' }))
  jiraTemplatesService.get.mockImplementation(() =>
    ok({ id: 'tpl-1', prompt_types: { items: prompts }, cmdb_types: { items: cmdbItems } }),
  )
  jiraTemplatesService.getAssetFieldConfigs.mockImplementation(() => ok({ items: fieldconfigs }))
  jiraTemplatesService.searchAssetObjects.mockImplementation(({ objectTypeId }) =>
    ok({ values: values[objectTypeId] ?? [], total: 0, has_next_page: false }),
  )
}

const state = () => useJiraPromptStore.getState()

beforeEach(() => {
  vi.clearAllMocks()
  state().reset()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('load: is the gate needed?', () => {
  it('no template on the resource: run now, nothing to add', async () => {
    seed()
    connectionsService.getConnection.mockImplementation(() => ok({ name: 'pg', jira_issue_template_id: '' }))
    await expect(state().load('pg')).resolves.toEqual({ needed: false, fields: {} })
    expect(jiraTemplatesService.get).not.toHaveBeenCalled()
  })

  it('integration not enabled: run now, nothing to add (no race with its fetch)', async () => {
    seed({ prompts: [prompt('customfield_1', 'text', true)] })
    jiraTemplatesService.getIntegration.mockImplementation(() => ok({ status: 'disabled' }))
    await expect(state().load('pg')).resolves.toEqual({ needed: false, fields: {} })
    jiraTemplatesService.getIntegration.mockImplementation(() => ok(null))
    await expect(state().load('pg')).resolves.toEqual({ needed: false, fields: {} })
    expect(jiraTemplatesService.get).not.toHaveBeenCalled()
  })

  it('a template with only mapping rules: run now, nothing to add', async () => {
    seed()
    await expect(state().load('pg')).resolves.toEqual({ needed: false, fields: {} })
  })

  it('a template with prompts: open the gate', async () => {
    seed({ prompts: [prompt('customfield_1', 'text')] })
    await expect(state().load('pg')).resolves.toEqual({ needed: true })
    expect(state().status).toBe(GATE_STATUS.READY)
  })

  it('CMDB presets all found and no prompt: run now with the presets (CLJS lost them)', async () => {
    seed({
      cmdbItems: [cmdb('customfield_2', '21', { value: 'db-prod-02', required: true })],
      values: { 21: servers },
    })
    await expect(state().load('pg')).resolves.toEqual({
      needed: false,
      fields: { jira_fields: { customfield_2: 'ari:server:2' } },
    })
  })

  it('a CMDB row without a preset: open the gate (CLJS sent nothing)', async () => {
    seed({ cmdbItems: [cmdb('customfield_2', '21')], values: { 21: servers } })
    await expect(state().load('pg')).resolves.toEqual({ needed: true })
  })

  it('a CMDB preset the values do not hold: open the gate', async () => {
    seed({ cmdbItems: [cmdb('customfield_2', '21', { value: 'db-prod-99' })], values: { 21: servers } })
    await expect(state().load('pg')).resolves.toEqual({ needed: true })
  })

  it('rejects when the resource cannot be read', async () => {
    seed()
    connectionsService.getConnection.mockImplementation(fail)
    await expect(state().load('pg')).rejects.toThrow('boom')
    expect(state().status).toBe(GATE_STATUS.IDLE)
  })

  it('rejects when the template cannot be read', async () => {
    seed()
    jiraTemplatesService.get.mockImplementation(fail)
    await expect(state().load('pg')).rejects.toThrow('boom')
  })

  it('a newer load replaces an older one, which resolves to null', async () => {
    seed({ prompts: [prompt('customfield_1', 'text')] })
    let release
    connectionsService.getConnection.mockImplementationOnce(
      () => new Promise((resolve) => (release = resolve)),
    )
    const first = state().load('old')
    const second = state().load('pg')
    release({ data: { name: 'old', jira_issue_template_id: 'tpl-1' } })
    await expect(first).resolves.toBeNull()
    await expect(second).resolves.toEqual({ needed: true })
  })
})

describe('CMDB error state', () => {
  it('a failed CMDB fetch opens the gate on the error; Try again keeps the answers', async () => {
    seed({
      prompts: [prompt('customfield_1', 'text', true)],
      cmdbItems: [cmdb('customfield_2', '21')],
      values: { 21: servers },
    })
    jiraTemplatesService.searchAssetObjects.mockImplementationOnce(fail)
    await expect(state().load('pg')).resolves.toEqual({ needed: true })
    expect(state().status).toBe(GATE_STATUS.ERROR)

    state().setAnswer('customfield_1', 'kept')
    await expect(state().retry()).resolves.toEqual({ needed: true })
    expect(state().status).toBe(GATE_STATUS.READY)
    expect(state().fields.customfield_1).toBe('kept')
    expect(state().rows[0].jira_values).toEqual(servers)
  })

  it('Try again that finds every preset and no prompt goes on without the form', async () => {
    seed({
      cmdbItems: [cmdb('customfield_2', '21', { value: 'db-prod-01' })],
      values: { 21: servers },
    })
    jiraTemplatesService.searchAssetObjects.mockImplementationOnce(fail)
    await expect(state().load('pg')).resolves.toEqual({ needed: true })
    await expect(state().retry()).resolves.toEqual({
      needed: false,
      fields: { jira_fields: { customfield_2: 'ari:server:1' } },
    })
  })

  it('a failed field config lookup is no error: the dropdowns stay unfiltered', async () => {
    seed({ cmdbItems: [cmdb('customfield_2', '21')], values: { 21: servers } })
    jiraTemplatesService.getAssetFieldConfigs.mockImplementation(fail)
    await state().load('pg')
    expect(state().status).toBe(GATE_STATUS.READY)
    expect(jiraTemplatesService.searchAssetObjects).toHaveBeenCalledWith(
      expect.objectContaining({ objectTypeId: '21', objectSchemaId: '7', aql: undefined }),
    )
  })

  it('a failed search shows the error state', async () => {
    vi.useFakeTimers()
    seed({ cmdbItems: [cmdb('customfield_2', '21')], values: { 21: servers } })
    await state().load('pg')
    jiraTemplatesService.searchAssetObjects.mockImplementationOnce(fail)
    state().setSearch('customfield_2', 'db')
    await vi.advanceTimersByTimeAsync(300)
    expect(state().status).toBe(GATE_STATUS.ERROR)
  })
})

describe('CMDB options', () => {
  it('search runs 300 ms after the last key, by name, from the first page', async () => {
    vi.useFakeTimers()
    seed({ cmdbItems: [cmdb('customfield_2', '21')], values: { 21: servers } })
    await state().load('pg')
    jiraTemplatesService.searchAssetObjects.mockClear()
    state().setSearch('customfield_2', 'd')
    state().setSearch('customfield_2', 'db-prod-0')
    await vi.advanceTimersByTimeAsync(299)
    expect(jiraTemplatesService.searchAssetObjects).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(jiraTemplatesService.searchAssetObjects).toHaveBeenCalledTimes(1)
    expect(jiraTemplatesService.searchAssetObjects).toHaveBeenCalledWith(
      expect.objectContaining({ name: 'db-prod-0', offset: 0, limit: 50 }),
    )
  })

  it('load more appends the next page', async () => {
    seed({ cmdbItems: [cmdb('customfield_2', '21')] })
    jiraTemplatesService.searchAssetObjects.mockImplementation(({ offset }) =>
      ok(
        offset === 0
          ? { values: [servers[0]], has_next_page: true }
          : { values: [servers[1]], has_next_page: false },
      ),
    )
    await state().load('pg')
    await state().loadMore('customfield_2')
    expect(jiraTemplatesService.searchAssetObjects).toHaveBeenLastCalledWith(
      expect.objectContaining({ offset: 50 }),
    )
    expect(state().rows[0].jira_values).toEqual(servers)
    expect(state().rows[0].hasMore).toBe(false)
  })

  it('an older response for a row does not replace a newer one', async () => {
    vi.useFakeTimers()
    seed({ cmdbItems: [cmdb('customfield_2', '21')], values: { 21: servers } })
    await state().load('pg')
    let releaseOld
    jiraTemplatesService.searchAssetObjects
      .mockImplementationOnce(() => new Promise((resolve) => (releaseOld = resolve)))
      .mockImplementationOnce(() => ok({ values: [servers[1]], has_next_page: false }))
    state().setSearch('customfield_2', 'a')
    await vi.advanceTimersByTimeAsync(300)
    state().setSearch('customfield_2', 'db-prod-02')
    await vi.advanceTimersByTimeAsync(300)
    releaseOld({ data: { values: [servers[0]], has_next_page: false } })
    await vi.runAllTimersAsync()
    expect(state().rows[0].jira_values).toEqual([servers[1]])
  })
})

describe('CMDB cascade', () => {
  const products = [
    { id: 'ari:product:1', name: 'Payments' },
    { id: 'ari:product:2', name: 'Ledger' },
  ]
  const components = [{ id: 'ari:component:1', name: 'API' }]
  const chain = {
    cmdbItems: [
      cmdb('customfield_20001', '11', { value: 'Payments' }),
      cmdb('customfield_20002', '12', { value: 'API' }),
    ],
    fieldconfigs: [
      {
        jira_field: 'customfield_20002',
        object_schema_id: '9',
        object_filter_query: 'objectType = "Component"',
        issue_scope_filter_query: 'Product = ${customfield_20001.label}',
      },
    ],
    values: { 11: products, 12: components },
  }

  it('a dependent row fetches with the AQL of the preset above it', async () => {
    seed(chain)
    await expect(state().load('pg')).resolves.toEqual({
      needed: false,
      fields: { jira_fields: { customfield_20001: 'ari:product:1', customfield_20002: 'ari:component:1' } },
    })
    expect(jiraTemplatesService.searchAssetObjects).toHaveBeenCalledWith(
      expect.objectContaining({
        objectTypeId: '12',
        objectSchemaId: '9',
        aql: '(objectType = "Component") AND (Product = "Payments")',
      }),
    )
  })

  it('a pick clears the dependent row and fetches it with the new filter', async () => {
    seed(chain)
    await state().load('pg')
    jiraTemplatesService.searchAssetObjects.mockClear()
    state().pickCmdb('customfield_20001', { value: 'ari:product:2', label: 'Ledger' })
    expect(state().fields).toEqual({ customfield_20001: { value: 'ari:product:2', label: 'Ledger' } })
    expect(state().rows[1]).toMatchObject({ value: null, selectedName: null, search: '' })
    expect(jiraTemplatesService.searchAssetObjects).toHaveBeenCalledWith(
      expect.objectContaining({ aql: '(objectType = "Component") AND (Product = "Ledger")' }),
    )
  })

  it('a row waiting on an upstream pick fetches nothing and offers nothing', async () => {
    seed({ ...chain, cmdbItems: [cmdb('customfield_20001', '11'), cmdb('customfield_20002', '12')] })
    await expect(state().load('pg')).resolves.toEqual({ needed: true })
    expect(jiraTemplatesService.searchAssetObjects).toHaveBeenCalledTimes(1)
    expect(state().rows[1].jira_values).toEqual([])
  })

  it('a failed cascade refetch is no error', async () => {
    seed(chain)
    await state().load('pg')
    jiraTemplatesService.searchAssetObjects.mockImplementationOnce(fail)
    state().pickCmdb('customfield_20001', { value: 'ari:product:2', label: 'Ledger' })
    await vi.waitFor(() => expect(state().rows[1].loading).toBe(false))
    expect(state().status).toBe(GATE_STATUS.READY)
  })
})

describe('Send', () => {
  it('a required field left empty blocks Send and is named', async () => {
    seed({
      prompts: [prompt('customfield_1', 'text', true), prompt('customfield_3', 'text')],
      cmdbItems: [cmdb('customfield_2', '21', { required: true })],
      values: { 21: servers },
    })
    await state().load('pg')
    expect(state().submit()).toBeNull()
    expect(state().missing).toEqual(['customfield_1', 'customfield_2'])

    state().setAnswer('customfield_1', 'reason')
    state().pickCmdb('customfield_2', { value: 'ari:server:1', label: 'db-prod-01' })
    expect(state().missing).toEqual([])
    expect(state().submit()).toEqual({
      jira_fields: { customfield_1: 'reason', customfield_2: 'ari:server:1' },
    })
  })

  it('a CMDB preset the values do not hold does not satisfy required (CLJS answered 422)', async () => {
    seed({
      cmdbItems: [cmdb('customfield_2', '21', { value: 'db-prod-99', required: true })],
      values: { 21: servers },
    })
    await state().load('pg')
    expect(state().submit()).toBeNull()
    expect(state().missing).toEqual(['customfield_2'])
  })

  it('the template reads required as the text "true", as the gateway does', async () => {
    seed({ prompts: [{ ...prompt('customfield_1', 'text'), required: 'false' }] })
    await state().load('pg')
    expect(state().submit()).toEqual({ jira_fields: {} })
  })
})
