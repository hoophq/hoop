import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// The axios instance is the seam: the real services run, so a test sees the
// response shape each one hands to the store.
const api = { get: vi.fn() }
vi.mock('@/services/api', () => ({ default: api }))
vi.mock('@/stores/useAuthStore', () => ({ useAuthStore: { subscribe: vi.fn() } }))

const { useJiraPromptStore, GATE_STATUS } = await import('@/stores/useJiraPromptStore')
const { promptOptions } = await import('@/features/JiraPromptGate/jiraFields')

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

// `value` left out means the template has no preset key at all.
const cmdb = (jiraField, objectType, { required = false, ...preset } = {}) => ({
  label: `Label of ${jiraField}`,
  jira_field: jiraField,
  jira_object_type: objectType,
  jira_object_schema_id: '7',
  required,
  ...preset,
})

// The gateway, per route. A test replaces one handler to change one answer.
let handlers

function seed({ prompts = [], cmdbItems = [], fieldconfigs = [], values = {} } = {}) {
  handlers = {
    connection: (name) => ok({ name, jira_issue_template_id: 'tpl-1' }),
    integration: () => ok({ status: 'enabled' }),
    template: (id) => ok({ id, prompt_types: { items: prompts }, cmdb_types: { items: cmdbItems } }),
    fieldconfigs: () => ok({ items: fieldconfigs }),
    objects: ({ object_type_id }) =>
      ok({ values: values[object_type_id] ?? [], total: 0, has_next_page: false }),
  }
}

api.get.mockImplementation((url, config = {}) => {
  if (url.startsWith('/connections/')) return handlers.connection(decodeURIComponent(url.slice(13)))
  if (url === '/integrations/jira') return handlers.integration()
  if (url.startsWith('/integrations/jira/issuetemplates/')) return handlers.template(url.split('/').pop())
  if (url === '/integrations/jira/assets/fieldconfigs') return handlers.fieldconfigs(config.params)
  if (url === '/integrations/jira/assets/objects') return handlers.objects(config.params)
  throw new Error(`unexpected GET ${url}`)
})

const objectCalls = () =>
  api.get.mock.calls.filter(([url]) => url === '/integrations/jira/assets/objects').map(([, c]) => c.params)
const templateCalls = () =>
  api.get.mock.calls.filter(([url]) => url.startsWith('/integrations/jira/issuetemplates/'))

const state = () => useJiraPromptStore.getState()

beforeEach(() => {
  api.get.mockClear()
  state().reset()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('load: is the gate needed?', () => {
  it('reads the template the resource names', async () => {
    seed({ prompts: [prompt('customfield_1', 'text')] })
    await expect(state().load('pg prod')).resolves.toEqual({ needed: true })
    expect(api.get).toHaveBeenCalledWith('/connections/pg%20prod')
    expect(api.get).toHaveBeenCalledWith('/integrations/jira/issuetemplates/tpl-1')
  })

  it('no template on the resource: run now, nothing to add', async () => {
    seed()
    handlers.connection = (name) => ok({ name, jira_issue_template_id: '' })
    await expect(state().load('pg')).resolves.toEqual({ needed: false, fields: {} })
    expect(templateCalls()).toHaveLength(0)
  })

  it('integration not enabled: run now, nothing to add (no race with its fetch)', async () => {
    seed({ prompts: [prompt('customfield_1', 'text', true)] })
    handlers.integration = () => ok({ status: 'disabled' })
    await expect(state().load('pg')).resolves.toEqual({ needed: false, fields: {} })
    handlers.integration = () => ok(null)
    await expect(state().load('pg')).resolves.toEqual({ needed: false, fields: {} })
    expect(templateCalls()).toHaveLength(0)
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

  it('a required CMDB row with no preset: open the gate (CLJS sent nothing, 422)', async () => {
    seed({ cmdbItems: [cmdb('customfield_2', '21', { required: true })], values: { 21: servers } })
    await expect(state().load('pg')).resolves.toEqual({ needed: true })
  })

  it('an optional CMDB row with no preset: run now without it, as CLJS', async () => {
    seed({ cmdbItems: [cmdb('customfield_2', '21')], values: { 21: servers } })
    await expect(state().load('pg')).resolves.toEqual({ needed: false, fields: {} })
  })

  it('an empty preset counts as one the values do not hold, as in CLJS', async () => {
    seed({ cmdbItems: [cmdb('customfield_2', '21', { value: '' })], values: { 21: servers } })
    await expect(state().load('pg')).resolves.toEqual({ needed: true })
  })

  it('a CMDB preset the values do not hold: open the gate', async () => {
    seed({ cmdbItems: [cmdb('customfield_2', '21', { value: 'db-prod-99' })], values: { 21: servers } })
    await expect(state().load('pg')).resolves.toEqual({ needed: true })
  })

  it('rejects when the resource cannot be read', async () => {
    seed()
    handlers.connection = fail
    await expect(state().load('pg')).rejects.toThrow('boom')
    expect(state().status).toBe(GATE_STATUS.IDLE)
  })

  it('rejects when the template cannot be read', async () => {
    seed()
    handlers.template = fail
    await expect(state().load('pg')).rejects.toThrow('boom')
  })

  it('a newer load replaces an older one, which resolves to null', async () => {
    seed({ prompts: [prompt('customfield_1', 'text')] })
    let release
    handlers.connection = () => new Promise((resolve) => (release = resolve))
    const first = state().load('old')
    handlers.connection = (name) => ok({ name, jira_issue_template_id: 'tpl-1' })
    const second = state().load('pg')
    release({ data: { name: 'old', jira_issue_template_id: 'tpl-1' } })
    await expect(first).resolves.toBeNull()
    await expect(second).resolves.toEqual({ needed: true })
  })
})

describe('CMDB error state', () => {
  it('a failed CMDB fetch opens the gate on the error; Try again shows the form', async () => {
    seed({
      prompts: [prompt('customfield_1', 'text', true)],
      cmdbItems: [cmdb('customfield_2', '21')],
      values: { 21: servers },
    })
    const objects = handlers.objects
    handlers.objects = fail
    await expect(state().load('pg')).resolves.toEqual({ needed: true })
    expect(state().status).toBe(GATE_STATUS.ERROR)

    handlers.objects = objects
    await expect(state().retry()).resolves.toEqual({ needed: true })
    expect(state().status).toBe(GATE_STATUS.READY)
    expect(state().rows[0].jira_values).toEqual(servers)
  })

  it('Try again that finds every preset and no prompt goes on without the form', async () => {
    seed({
      cmdbItems: [cmdb('customfield_2', '21', { value: 'db-prod-01' })],
      values: { 21: servers },
    })
    const objects = handlers.objects
    handlers.objects = fail
    await expect(state().load('pg')).resolves.toEqual({ needed: true })
    handlers.objects = objects
    await expect(state().retry()).resolves.toEqual({
      needed: false,
      fields: { jira_fields: { customfield_2: 'ari:server:1' } },
    })
  })

  it('Try again before any form takes presets from the fresh options only', async () => {
    const products = [{ id: 'ari:product:1', name: 'Payments' }]
    seed({
      cmdbItems: [
        cmdb('customfield_30001', '21', { value: 'db-prod-01', required: true }),
        cmdb('customfield_20001', '11', { value: 'Payments' }),
      ],
      values: { 21: servers, 11: products },
    })
    const objects = handlers.objects
    handlers.objects = (params) => (params.object_type_id === '11' ? fail() : objects(params))
    await expect(state().load('pg')).resolves.toEqual({ needed: true })
    expect(state().fields.customfield_30001).toEqual({ value: 'ari:server:1', label: 'db-prod-01' })

    // db-prod-01 left Jira before the user tried again.
    handlers.objects = (params) =>
      params.object_type_id === '21' ? ok({ values: [servers[1]], has_next_page: false }) : objects(params)
    await expect(state().retry()).resolves.toEqual({ needed: true })
    expect(state().fields.customfield_30001).toBeUndefined()
  })

  it('Cancel during Try again: the retry resolves to null, so nothing is sent', async () => {
    seed({
      cmdbItems: [cmdb('customfield_2', '21', { value: 'db-prod-01', required: true })],
      values: { 21: servers },
    })
    const objects = handlers.objects
    handlers.objects = fail
    await state().load('pg')
    let release
    handlers.objects = () => new Promise((resolve) => (release = resolve))
    const retry = state().retry()
    state().abandon()
    release(await objects({ object_type_id: '21' }))
    await expect(retry).resolves.toBeNull()
  })

  it('Try again after an error in the open form goes back to the form, never sends', async () => {
    vi.useFakeTimers()
    const products = [{ id: 'ari:product:2', name: 'Ledger' }]
    seed({
      cmdbItems: [
        cmdb('customfield_30001', '21', { value: 'db-prod-01', required: true }),
        cmdb('customfield_20001', '11', { value: '', required: true }),
      ],
      values: { 21: servers, 11: products },
    })
    await expect(state().load('pg')).resolves.toEqual({ needed: true })
    // Every row resolved now: a load-time decision would say "no form".
    state().pickCmdb('customfield_20001', { value: 'ari:product:2', label: 'Ledger' })
    const objects = handlers.objects
    handlers.objects = fail
    state().setSearch('customfield_30001', 'db')
    await vi.advanceTimersByTimeAsync(300)
    expect(state().status).toBe(GATE_STATUS.ERROR)

    handlers.objects = objects
    await expect(state().retry()).resolves.toEqual({ needed: true })
    expect(state().status).toBe(GATE_STATUS.READY)
    expect(state().fields.customfield_20001).toEqual({ value: 'ari:product:2', label: 'Ledger' })
  })

  it('a failed field config lookup is no error: the dropdowns stay unfiltered', async () => {
    seed({ cmdbItems: [cmdb('customfield_2', '21', { required: true })], values: { 21: servers } })
    handlers.fieldconfigs = fail
    await state().load('pg')
    expect(state().status).toBe(GATE_STATUS.READY)
    expect(objectCalls()).toEqual([
      { object_type_id: '21', object_schema_id: '7', limit: 50, offset: 0 },
    ])
  })

  it('a failed search shows the error state', async () => {
    vi.useFakeTimers()
    seed({ cmdbItems: [cmdb('customfield_2', '21', { required: true })], values: { 21: servers } })
    await state().load('pg')
    handlers.objects = fail
    state().setSearch('customfield_2', 'db')
    await vi.advanceTimersByTimeAsync(300)
    expect(state().status).toBe(GATE_STATUS.ERROR)
  })
})

describe('CMDB options', () => {
  it('search runs 300 ms after the last key, by name, from the first page', async () => {
    vi.useFakeTimers()
    seed({ cmdbItems: [cmdb('customfield_2', '21', { required: true })], values: { 21: servers } })
    await state().load('pg')
    api.get.mockClear()
    state().setSearch('customfield_2', 'd')
    state().setSearch('customfield_2', 'db-prod-0')
    await vi.advanceTimersByTimeAsync(299)
    expect(objectCalls()).toHaveLength(0)
    await vi.advanceTimersByTimeAsync(1)
    expect(objectCalls()).toEqual([
      { object_type_id: '21', object_schema_id: '7', name: 'db-prod-0', limit: 50, offset: 0 },
    ])
  })

  it('load more appends the next page', async () => {
    seed({ cmdbItems: [cmdb('customfield_2', '21', { required: true })] })
    handlers.objects = ({ offset }) =>
      ok(
        offset === 0
          ? { values: [servers[0]], has_next_page: true }
          : { values: [servers[1]], has_next_page: false },
      )
    await state().load('pg')
    await state().loadMore('customfield_2')
    expect(objectCalls().at(-1)).toMatchObject({ offset: 50 })
    expect(state().rows[0].jira_values).toEqual(servers)
    expect(state().rows[0].hasMore).toBe(false)
  })

  it('an older response for a row does not replace a newer one', async () => {
    vi.useFakeTimers()
    seed({ cmdbItems: [cmdb('customfield_2', '21', { required: true })], values: { 21: servers } })
    await state().load('pg')
    let releaseOld
    handlers.objects = () => new Promise((resolve) => (releaseOld = resolve))
    state().setSearch('customfield_2', 'a')
    await vi.advanceTimersByTimeAsync(300)
    handlers.objects = () => ok({ values: [servers[1]], has_next_page: false })
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

  it('asks for the field configs of every CMDB row once', async () => {
    seed(chain)
    await state().load('pg')
    expect(api.get).toHaveBeenCalledWith('/integrations/jira/assets/fieldconfigs', {
      params: { jira_fields: 'customfield_20001,customfield_20002' },
    })
  })

  it('a dependent row fetches with the AQL of the preset above it', async () => {
    seed(chain)
    await expect(state().load('pg')).resolves.toEqual({
      needed: false,
      fields: { jira_fields: { customfield_20001: 'ari:product:1', customfield_20002: 'ari:component:1' } },
    })
    expect(objectCalls()).toContainEqual({
      object_type_id: '12',
      object_schema_id: '9',
      aql: '(objectType = "Component") AND (Product = "Payments")',
      limit: 50,
      offset: 0,
    })
  })

  it('a pick clears the dependent row and fetches it with the new filter', async () => {
    seed(chain)
    await state().load('pg')
    api.get.mockClear()
    state().pickCmdb('customfield_20001', { value: 'ari:product:2', label: 'Ledger' })
    expect(state().fields).toEqual({ customfield_20001: { value: 'ari:product:2', label: 'Ledger' } })
    expect(state().rows[1]).toMatchObject({ value: null, selectedName: null, search: '' })
    expect(objectCalls()).toEqual([
      expect.objectContaining({ aql: '(objectType = "Component") AND (Product = "Ledger")' }),
    ])
  })

  it('a row waiting on an upstream pick fetches nothing and offers nothing', async () => {
    seed({
      ...chain,
      cmdbItems: [cmdb('customfield_20001', '11', { value: '' }), cmdb('customfield_20002', '12', { value: '' })],
    })
    await expect(state().load('pg')).resolves.toEqual({ needed: true })
    expect(objectCalls()).toHaveLength(1)
    expect(state().rows[1].jira_values).toEqual([])
  })

  it('a failed cascade refetch is no error: the row says so and retries when opened', async () => {
    seed(chain)
    await state().load('pg')
    const objects = handlers.objects
    handlers.objects = fail
    state().pickCmdb('customfield_20001', { value: 'ari:product:2', label: 'Ledger' })
    await vi.waitFor(() => expect(state().rows[1].loading).toBe(false))
    expect(state().status).toBe(GATE_STATUS.READY)
    expect(state().rows[1].failed).toBe(true)

    handlers.objects = objects
    await state().retryRow('customfield_20002')
    expect(state().rows[1]).toMatchObject({ failed: false, jira_values: components })
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

describe('select prompt options', () => {
  it('drops duplicates, which Mantine Select rejects', () => {
    expect(promptOptions({ field_options: ['low', 'low', 'high'] })).toEqual(['low', 'high'])
    expect(promptOptions({})).toEqual([])
  })
})
