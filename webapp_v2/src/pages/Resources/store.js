import { create } from 'zustand'
import { resourcesService } from '@/services/resources'
import { connectionsService } from '@/services/connections'
import { connectionTagsService } from '@/services/connectionTags'
import { attributesService } from '@/services/attributes'
import { useAuthStore } from '@/stores/useAuthStore'
import { tagsToSelector } from '@/pages/Resources/utils'

export const PAGE_SIZE = 50
export const TABS = { RESOURCES: 'resources', ROLES: 'roles' }
export const MIN_SEARCH_LENGTH = 3

export const EMPTY_FILTERS = { search: '', tags: {}, type: null, attribute: null }
const emptyList = { items: [], total: 0, page: 0, loading: false, loaded: false, error: null, key: null }

const emptyTest = {
  opened: false,
  connectionName: null,
  testing: false,
  agentStatus: 'checking',
  connectionStatus: 'checking',
  durationMs: null,
  errorMessage: null,
}

// Module-level so a response that outlives reset() is still recognised as stale.
const generation = { [TABS.RESOURCES]: 0, [TABS.ROLES]: 0 }

// Every request carries every active filter (Q2).
export function listParams(tab, filters, page) {
  const params = { page, page_size: PAGE_SIZE }
  const search = filters.search.trim()
  if (search.length >= MIN_SEARCH_LENGTH) params.search = search
  if (filters.type) params.subtype = filters.type
  if (tab === TABS.ROLES) {
    const selector = tagsToSelector(filters.tags)
    if (selector) params.tag_selector = selector
    if (filters.attribute) params.attribute = filters.attribute
  }
  return params
}

export function hasMore(list) {
  return list.page * PAGE_SIZE < list.total
}

export function hasFilters(filters) {
  return Object.keys(filters.tags).length > 0 || Boolean(filters.type) || Boolean(filters.attribute)
}

const fetchers = {
  [TABS.RESOURCES]: (params) => resourcesService.list(params),
  [TABS.ROLES]: (params) => connectionsService.list(params),
}

export const useResourcesStore = create((set, get) => {
  const patchList = (tab, patch) =>
    set((state) => ({ lists: { ...state.lists, [tab]: { ...state.lists[tab], ...patch } } }))

  return {
    tab: TABS.ROLES,
    filters: EMPTY_FILTERS,
    // What the search box shows; `filters.search` is what the lists use.
    searchInput: '',
    lists: { [TABS.RESOURCES]: emptyList, [TABS.ROLES]: emptyList },
    tags: [],
    attributes: [],
    test: emptyTest,

    // Both lists load on open: the header button depends on the resources list.
    open: (tab) => {
      set({ tab, filters: EMPTY_FILTERS, searchInput: '' })
      get().load(TABS.RESOURCES)
      get().load(TABS.ROLES)
      get().loadTags()
      get().loadAttributes()
    },

    switchTab: (tab) => {
      if (tab === get().tab) return
      set({ tab, filters: EMPTY_FILTERS, searchInput: '' })
      get().load(tab)
    },

    setSearchInput: (searchInput) => set({ searchInput }),

    setFilters: (patch) => {
      set((state) => ({ filters: { ...state.filters, ...patch } }))
      get().load(get().tab)
    },

    clearFilters: () => {
      set({ filters: EMPTY_FILTERS, searchInput: '' })
      get().load(get().tab)
    },

    // The last request wins (Q3). A first page with new params drops the old
    // rows, so the loader shows instead of rows from another query; the same
    // params keep them on screen while they refresh.
    load: async (tab, { append = false } = {}) => {
      const current = get().lists[tab]
      const page = append ? current.page + 1 : 1
      const params = listParams(tab, get().filters, page)
      const key = JSON.stringify({ ...params, page: undefined })
      const gen = ++generation[tab]
      patchList(tab, {
        loading: true,
        error: null,
        ...(!append && key !== current.key ? { items: [], total: 0, page: 0 } : {}),
      })
      try {
        const { data } = await fetchers[tab](params)
        if (gen !== generation[tab]) return
        const rows = data?.data ?? []
        patchList(tab, {
          items: append ? [...get().lists[tab].items, ...rows] : rows,
          total: data?.pages?.total ?? rows.length,
          page,
          key,
          loading: false,
          loaded: true,
        })
      } catch (err) {
        if (gen !== generation[tab]) return
        patchList(tab, {
          loading: false,
          loaded: true,
          error: err?.response?.data?.message || 'Failed to load the list.',
        })
      }
    },

    // One request per page: no second request while one is in flight (Q1).
    loadMore: (tab) => {
      const list = get().lists[tab]
      if (list.loading || !list.loaded || !hasMore(list)) return
      get().load(tab, { append: true })
    },

    loadTags: async () => {
      try {
        const data = await connectionTagsService.list()
        set({ tags: data?.items ?? [] })
      } catch {
        set({ tags: [] })
      }
    },

    loadAttributes: async () => {
      try {
        set({ attributes: await attributesService.listAll() })
      } catch {
        set({ attributes: [] })
      }
    },

    // Stays on the page and reloads after the DELETE succeeds, with the
    // filters in place (Q4).
    deleteRole: async (name) => {
      await connectionsService.deleteConnection(name)
      await get().load(TABS.ROLES)
    },

    testConnection: async (name) => {
      const startedAt = Date.now()
      set({ test: { ...emptyTest, opened: true, connectionName: name, testing: true } })
      const settle = (patch) =>
        set((state) => {
          if (state.test.connectionName !== name) return {}
          const test = { ...state.test, ...patch }
          if (test.agentStatus !== 'checking' && test.connectionStatus !== 'checking') {
            test.durationMs = Date.now() - startedAt
          }
          return { test }
        })
      const agent = connectionsService
        .getConnection(name)
        .then((data) => settle({ agentStatus: data?.status === 'online' ? 'online' : 'offline' }))
        .catch(() => settle({ agentStatus: 'offline' }))
      const probe = connectionsService
        .testConnection(name)
        .then(() => settle({ connectionStatus: 'successful' }))
        .catch((err) =>
          settle({
            connectionStatus: 'failed',
            errorMessage: err?.response?.data?.message || 'Connection test failed.',
          }),
        )
      await Promise.all([agent, probe])
      settle({ testing: false })
    },

    closeTest: () => set({ test: emptyTest }),

    reset: () =>
      set({
        tab: TABS.ROLES,
        filters: EMPTY_FILTERS,
        searchInput: '',
        lists: { [TABS.RESOURCES]: emptyList, [TABS.ROLES]: emptyList },
        tags: [],
        attributes: [],
        test: emptyTest,
      }),
  }
})

useAuthStore.subscribe((state, prev) => {
  if (prev.isAuthenticated && !state.isAuthenticated) useResourcesStore.getState().reset()
})
