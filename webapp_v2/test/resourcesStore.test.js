import { beforeEach, describe, expect, it, vi } from 'vitest'

const resourcesService = { list: vi.fn() }
const connectionsService = { list: vi.fn(), deleteConnection: vi.fn() }

vi.mock('@/services/resources', () => ({ resourcesService }))
vi.mock('@/services/connections', () => ({ connectionsService }))
vi.mock('@/services/connectionTags', () => ({ connectionTagsService: { list: vi.fn(async () => ({ items: [] })) } }))
vi.mock('@/services/attributes', () => ({ attributesService: { listAll: vi.fn(async () => []) } }))
vi.mock('@/stores/useAuthStore', () => ({ useAuthStore: { subscribe: () => () => {} } }))

const { useResourcesStore, listParams, TABS, EMPTY_FILTERS } = await import('@/pages/Resources/store')
const { displayKey, tagsToSelector } = await import('@/pages/Resources/utils')

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
})
