import { beforeEach, describe, expect, it, vi } from 'vitest'

const resourcesService = { list: vi.fn(), get: vi.fn(), update: vi.fn(), remove: vi.fn() }
const connectionsService = { list: vi.fn(), deleteConnection: vi.fn() }

vi.mock('@/services/resources', () => ({ resourcesService }))
vi.mock('@/services/connections', () => ({ connectionsService }))
vi.mock('@/services/connectionTags', () => ({ connectionTagsService: { list: vi.fn() } }))
vi.mock('@/services/attributes', () => ({ attributesService: { listAll: vi.fn() } }))
vi.mock('@/stores/useAuthStore', () => ({ useAuthStore: { subscribe: () => () => {} } }))

const { roleCountKnown, useConfigureResourceStore } = await import('@/pages/Resources/Configure/store')

const resource = { id: 'r1', name: 'pg', type: 'database', subtype: 'postgres', agent_id: 'a1', env_vars: { HOST: 'x' } }
const page = (names, total = names.length) => ({
  data: { pages: { total }, data: names.map((name) => ({ id: name, name })) },
})
const store = () => useConfigureResourceStore.getState()

function deferred() {
  let resolve
  const promise = new Promise((r) => {
    resolve = r
  })
  return { promise, resolve }
}

beforeEach(() => {
  vi.clearAllMocks()
  store().reset()
})

describe('open', () => {
  it('loads the resource and its roles by resource name', async () => {
    resourcesService.get.mockResolvedValue({ data: resource })
    connectionsService.list.mockResolvedValue(page(['pg-ro', 'pg-rw']))
    store().open('pg')
    await vi.waitFor(() => expect(store().roles.loaded).toBe(true))
    expect(resourcesService.get).toHaveBeenCalledWith('pg')
    expect(connectionsService.list).toHaveBeenCalledWith({ resource_name: 'pg', page: 1, page_size: 50 })
    expect(store().resource).toEqual(resource)
    expect(store().roles.total).toBe(2)
  })

  it('ends on not-found for an unknown name, not on a loader (Q8)', async () => {
    resourcesService.get.mockRejectedValue({ response: { status: 404, data: { message: 'resource not found' } } })
    connectionsService.list.mockResolvedValue(page([]))
    store().open('nope')
    await vi.waitFor(() => expect(store().loading).toBe(false))
    expect(store().notFound).toBe(true)
    expect(store().error).toBe(null)
  })

  it('ignores a roles response from the previous resource', async () => {
    const first = deferred()
    resourcesService.get.mockResolvedValue({ data: resource })
    connectionsService.list.mockReturnValueOnce(first.promise).mockResolvedValueOnce(page(['b-role']))
    store().open('a')
    store().open('b')
    await vi.waitFor(() => expect(store().roles.loaded).toBe(true))
    first.resolve(page(['a-role']))
    await first.promise
    expect(store().roles.items.map((r) => r.name)).toEqual(['b-role'])
  })
})

describe('rename', () => {
  it('sends the whole resource with the new name and reloads the roles under it (Q7)', async () => {
    resourcesService.get.mockResolvedValue({ data: resource })
    connectionsService.list.mockResolvedValue(page([]))
    store().open('pg')
    await vi.waitFor(() => expect(store().resource).toEqual(resource))
    resourcesService.update.mockResolvedValue({ data: {} })
    await store().rename('pg-new')
    expect(resourcesService.update).toHaveBeenCalledWith('pg', {
      name: 'pg-new',
      type: 'database',
      subtype: 'postgres',
      agent_id: 'a1',
      env_vars: { HOST: 'x' },
    })
    expect(store().name).toBe('pg-new')
    expect(store().resource.name).toBe('pg-new')
    expect(connectionsService.list).toHaveBeenLastCalledWith({ resource_name: 'pg-new', page: 1, page_size: 50 })
    expect(store().saving).toBe(false)
  })
})

describe('stale completions', () => {
  async function opened() {
    resourcesService.get.mockResolvedValue({ data: resource })
    connectionsService.list.mockResolvedValue(page([]))
    store().open('pg')
    await vi.waitFor(() => expect(store().resource).toEqual(resource))
  }

  it('rename resolves false and changes nothing when the page was left during the PUT', async () => {
    await opened()
    const put = deferred()
    resourcesService.update.mockReturnValue(put.promise)
    const done = store().rename('pg-new')
    store().reset()
    put.resolve({ data: {} })
    expect(await done).toBe(false)
    expect(store().name).toBe(null)
  })

  it('deleteResource resolves false when the page was left during the DELETE', async () => {
    await opened()
    const del = deferred()
    resourcesService.remove.mockReturnValue(del.promise)
    const done = store().deleteResource()
    store().reset()
    del.resolve({})
    expect(await done).toBe(false)
  })

  it('deleteResource resolves true on the same page', async () => {
    await opened()
    resourcesService.remove.mockResolvedValue({})
    expect(await store().deleteResource()).toBe(true)
    expect(resourcesService.remove).toHaveBeenCalledWith('pg')
  })
})

describe('roleCountKnown', () => {
  it('is false while no roles response succeeded, so Delete cannot trust a zero count', async () => {
    resourcesService.get.mockResolvedValue({ data: resource })
    connectionsService.list.mockRejectedValue({ response: { data: { message: 'boom' } } })
    store().open('pg')
    await vi.waitFor(() => expect(store().roles.loaded).toBe(true))
    expect(store().roles.total).toBe(0)
    expect(roleCountKnown(store().roles)).toBe(false)
    connectionsService.list.mockResolvedValue(page(['pg-ro']))
    await store().retryRoles()
    expect(roleCountKnown(store().roles)).toBe(true)
  })
})

describe('deleteRole', () => {
  it('reloads the roles only after the DELETE ends (Q4)', async () => {
    resourcesService.get.mockResolvedValue({ data: resource })
    connectionsService.list.mockResolvedValue(page(['pg-ro', 'pg-rw']))
    store().open('pg')
    await vi.waitFor(() => expect(store().roles.loaded).toBe(true))
    const del = deferred()
    connectionsService.deleteConnection.mockReturnValue(del.promise)
    connectionsService.list.mockClear()
    const done = store().deleteRole('pg-ro')
    expect(connectionsService.list).not.toHaveBeenCalled()
    connectionsService.list.mockResolvedValue(page(['pg-rw']))
    del.resolve()
    await done
    expect(connectionsService.list).toHaveBeenCalledTimes(1)
    expect(store().roles.items.map((r) => r.name)).toEqual(['pg-rw'])
  })

  it('drops the deleted row when the reload fails', async () => {
    resourcesService.get.mockResolvedValue({ data: resource })
    connectionsService.list.mockResolvedValue(page(['pg-ro', 'pg-rw']))
    store().open('pg')
    await vi.waitFor(() => expect(store().roles.loaded).toBe(true))
    connectionsService.deleteConnection.mockResolvedValue({})
    connectionsService.list.mockRejectedValue({ response: { data: { message: 'boom' } } })
    await store().deleteRole('pg-ro')
    expect(store().roles.items.map((r) => r.name)).toEqual(['pg-rw'])
    expect(store().roles.total).toBe(1)
    expect(store().roles.error).toBe('boom')
  })
})

describe('loadMoreRoles', () => {
  it('asks for the next page only while more remain', async () => {
    resourcesService.get.mockResolvedValue({ data: resource })
    connectionsService.list.mockResolvedValue(page(['r1'], 51))
    store().open('pg')
    await vi.waitFor(() => expect(store().roles.loaded).toBe(true))
    connectionsService.list.mockResolvedValue(page(['r2'], 51))
    await store().loadMoreRoles()
    expect(connectionsService.list).toHaveBeenLastCalledWith({ resource_name: 'pg', page: 2, page_size: 50 })
    expect(store().roles.items.map((r) => r.name)).toEqual(['r1', 'r2'])
    expect(store().loadMoreRoles()).toBeUndefined()
  })
})
