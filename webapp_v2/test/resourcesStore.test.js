import { beforeEach, describe, expect, it, vi } from 'vitest'

const resourcesService = { list: vi.fn() }
const connectionsService = { list: vi.fn(), deleteConnection: vi.fn() }

vi.mock('@/services/resources', () => ({ resourcesService }))
vi.mock('@/services/connections', () => ({ connectionsService }))
vi.mock('@/services/connectionTags', () => ({ connectionTagsService: { list: vi.fn(async () => ({ items: [] })) } }))
vi.mock('@/services/attributes', () => ({ attributesService: { listAll: vi.fn(async () => []) } }))
vi.mock('@/stores/useAuthStore', () => ({ useAuthStore: { subscribe: () => () => {} } }))

const { useResourcesStore, listParams, TABS, EMPTY_FILTERS } = await import('@/pages/Resources/store')
const { displayKey, groupTags, selectedValues, tagsToSelector } = await import('@/pages/Resources/utils')

const page = (names, total = names.length) => ({
  data: { pages: { total }, data: names.map((name) => ({ id: name, name })) },
})

function deferred() {
  let resolve
  const promise = new Promise((r) => {
    resolve = r
  })
  return { promise, resolve }
}

beforeEach(() => {
  vi.clearAllMocks()
  useResourcesStore.getState().reset()
})

describe('listParams', () => {
  it('sends every active filter on the roles list (Q2)', () => {
    const filters = { search: 'postgres', tags: { env: ['prod', 'qa'] }, type: 'postgres', attribute: 'pii' }
    expect(listParams(TABS.ROLES, filters, 2)).toEqual({
      page: 2,
      page_size: 50,
      search: 'postgres',
      subtype: 'postgres',
      tag_selector: 'env=prod,env=qa',
      attribute: 'pii',
    })
  })

  it('keeps search and type on the resources list, and drops a search under 3 characters', () => {
    expect(listParams(TABS.RESOURCES, { ...EMPTY_FILTERS, search: 'my', type: 'ssh' }, 1)).toEqual({
      page: 1,
      page_size: 50,
      subtype: 'ssh',
    })
  })
})

describe('resources store', () => {
  it('keeps the newest response when an older one lands last (Q3)', async () => {
    const slow = deferred()
    resourcesService.list.mockReturnValueOnce(slow.promise).mockResolvedValueOnce(page(['postgres-db']))
    const store = useResourcesStore.getState()
    useResourcesStore.setState({ tab: TABS.RESOURCES })
    const first = store.load(TABS.RESOURCES)
    store.setFilters({ search: 'postgres' })
    await vi.waitFor(() => expect(useResourcesStore.getState().lists.resources.loading).toBe(false))
    slow.resolve(page(['stale']))
    await first
    expect(useResourcesStore.getState().lists.resources.items.map((r) => r.name)).toEqual(['postgres-db'])
  })

  it('asks for one page at a time and appends it with the same filters (Q1, Q2)', async () => {
    const next = deferred()
    connectionsService.list
      .mockResolvedValueOnce(page(Array.from({ length: 50 }, (_, i) => 'r' + i), 60))
      .mockReturnValueOnce(next.promise)
    const store = useResourcesStore.getState()
    useResourcesStore.setState({ filters: { ...EMPTY_FILTERS, type: 'postgres' } })
    await store.load(TABS.ROLES)
    store.loadMore(TABS.ROLES)
    store.loadMore(TABS.ROLES)
    expect(connectionsService.list).toHaveBeenCalledTimes(2)
    expect(connectionsService.list.mock.calls[1][0]).toMatchObject({ page: 2, subtype: 'postgres' })
    next.resolve(page(['r50'], 60))
    await vi.waitFor(() => expect(useResourcesStore.getState().lists.roles.items).toHaveLength(51))
  })

  it('does not ask for the next page again after it failed, until a retry', async () => {
    connectionsService.list
      .mockResolvedValueOnce(page(Array.from({ length: 50 }, (_, i) => 'r' + i), 60))
      .mockRejectedValueOnce({ response: { data: { message: 'bad gateway' } } })
      .mockResolvedValueOnce(page(['r50'], 60))
    const store = useResourcesStore.getState()
    await store.load(TABS.ROLES)
    await store.loadMore(TABS.ROLES)
    expect(useResourcesStore.getState().lists.roles.error).toBe('bad gateway')
    store.loadMore(TABS.ROLES)
    expect(connectionsService.list).toHaveBeenCalledTimes(2)
    await store.retry(TABS.ROLES)
    expect(connectionsService.list.mock.calls[2][0]).toMatchObject({ page: 2 })
    expect(useResourcesStore.getState().lists.roles.items).toHaveLength(51)
  })

  it('keeps the store empty when a list answers after logout', async () => {
    const slow = deferred()
    connectionsService.list.mockReturnValueOnce(slow.promise)
    const pending = useResourcesStore.getState().load(TABS.ROLES)
    useResourcesStore.getState().reset()
    slow.resolve(page(['previous-user-role']))
    await pending
    expect(useResourcesStore.getState().lists.roles.items).toEqual([])
  })

  it('drops the deleted row even when the reload fails', async () => {
    connectionsService.list
      .mockResolvedValueOnce(page(['pg-ro', 'pg-rw'], 2))
      .mockRejectedValueOnce({ response: { data: { message: 'bad gateway' } } })
    connectionsService.deleteConnection.mockResolvedValueOnce({})
    await useResourcesStore.getState().load(TABS.ROLES)
    await useResourcesStore.getState().deleteRole('pg-ro')
    const roles = useResourcesStore.getState().lists.roles
    expect(roles.items.map((r) => r.name)).toEqual(['pg-rw'])
    expect(roles.total).toBe(1)
    expect(roles.error).toBe('bad gateway')
  })

  it('ignores a late result from an earlier test of the same role', async () => {
    const firstAgent = deferred()
    const firstProbe = deferred()
    connectionsService.getConnection = vi
      .fn()
      .mockReturnValueOnce(firstAgent.promise)
      .mockResolvedValueOnce({ status: 'online' })
    connectionsService.testConnection = vi
      .fn()
      .mockReturnValueOnce(firstProbe.promise)
      .mockReturnValueOnce(new Promise(() => {}))
    const store = useResourcesStore.getState()
    const first = store.testConnection('pg-ro')
    store.closeTest()
    store.testConnection('pg-ro')
    firstAgent.resolve({ status: 'offline' })
    firstProbe.resolve({})
    await first
    const { test } = useResourcesStore.getState()
    expect(test.agentStatus).toBe('online')
    expect(test.connectionStatus).toBe('checking')
    expect(test.testing).toBe(true)
  })

  it('reloads the roles after the DELETE succeeds, with the filters kept (Q4)', async () => {
    const order = []
    connectionsService.deleteConnection.mockImplementation(async () => order.push('delete'))
    connectionsService.list.mockImplementation(async (params) => {
      order.push('list ' + params.tag_selector)
      return page([])
    })
    useResourcesStore.setState({ filters: { ...EMPTY_FILTERS, tags: { env: ['prod'] } } })
    await useResourcesStore.getState().deleteRole('pg-ro')
    expect(order).toEqual(['delete', 'list env=prod'])
  })
})

describe('tag helpers', () => {
  it('builds the tag selector and the display key like the CLJS selector', () => {
    expect(tagsToSelector({ env: ['prod'], 'hoop.dev/infrastructure.environment': ['dev'] })).toBe(
      'env=prod,hoop.dev/infrastructure.environment=dev',
    )
    expect(displayKey('hoop.dev/infrastructure.environment')).toBe('environment')
    expect(displayKey('env')).toBe('env')
  })

  it('groups a tag key that names an Object.prototype member', () => {
    const grouped = groupTags([{ key: 'constructor', value: 'a' }, { key: 'constructor', value: 'a' }])
    expect(grouped.constructor).toEqual(['a'])
    expect(selectedValues({}, 'constructor')).toEqual([])
  })
})
