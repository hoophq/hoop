import { create } from 'zustand'
import { eventRoutingService } from '@/services/eventRouting'
import { connectionsService } from '@/services/connections'
import { runbooksService } from '@/services/runbooks'

const initialState = {
  subscriptions: { status: 'idle', data: [], error: null },
  catalog: { status: 'idle', data: [], error: null },
  connections: { status: 'idle', data: [] },
  dispatches: {}, // { [subId]: { status, data, error } }
  // Runbooks available per connection: { [connectionName]: { status, data, error } }
  // data is the raw repositories array (each entry has { repository, commit, items[] }).
  runbooksByConnection: {},
  activeTab: 'subscriptions',
  search: '',
  statusFilter: 'all',
  catalogSearch: '',
  catalogFilter: 'all',
  submitting: false,
  // transient modal targets
  replayTarget: null,
  eventDetailTarget: null,
}

export const useEventRoutingStore = create((set, get) => ({
  ...initialState,

  setActiveTab: (tab) => set({ activeTab: tab }),
  setSearch: (q) => set({ search: q }),
  setStatusFilter: (s) => set({ statusFilter: s }),
  setCatalogSearch: (q) => set({ catalogSearch: q }),
  setCatalogFilter: (c) => set({ catalogFilter: c }),

  _setReplayTarget: (d) => set({ replayTarget: d }),
  _setEventDetailTarget: (e) => set({ eventDetailTarget: e }),

  fetchRunbooksForConnection: async (connectionName) => {
    if (!connectionName) return
    set((state) => ({
      runbooksByConnection: {
        ...state.runbooksByConnection,
        [connectionName]: {
          status: 'loading',
          data: state.runbooksByConnection[connectionName]?.data || [],
          error: null,
        },
      },
    }))
    try {
      const repos = await runbooksService.listForConnection(connectionName)
      set((state) => ({
        runbooksByConnection: {
          ...state.runbooksByConnection,
          [connectionName]: { status: 'ready', data: repos, error: null },
        },
      }))
    } catch (error) {
      const msg =
        error?.response?.data?.message || error?.message || 'Failed to load runbooks'
      set((state) => ({
        runbooksByConnection: {
          ...state.runbooksByConnection,
          [connectionName]: { status: 'error', data: [], error: msg },
        },
      }))
    }
  },

  fetchAll: async () => {
    set({
      subscriptions: { status: 'loading', data: get().subscriptions.data, error: null },
      catalog: { status: 'loading', data: get().catalog.data, error: null },
      connections: { status: 'loading', data: get().connections.data },
    })
    try {
      const [subs, catalog, conns] = await Promise.all([
        eventRoutingService.listSubscriptions(),
        eventRoutingService.listCatalog(),
        connectionsService.getConnections().catch(() => []),
      ])
      const connList = Array.isArray(conns) ? conns : (conns?.items ?? [])
      set({
        subscriptions: { status: 'ready', data: subs, error: null },
        catalog: { status: 'ready', data: catalog, error: null },
        connections: { status: 'ready', data: connList },
      })
    } catch (error) {
      const msg = error?.response?.data?.message || error?.message || 'Failed to load event routing data'
      set({
        subscriptions: { status: 'error', data: [], error: msg },
        catalog: { status: 'error', data: [], error: msg },
        connections: { status: 'error', data: [] },
      })
    }
  },

  fetchDispatches: async (subId) => {
    set((state) => ({
      dispatches: {
        ...state.dispatches,
        [subId]: { status: 'loading', data: state.dispatches[subId]?.data || [], error: null },
      },
    }))
    try {
      const result = await eventRoutingService.listDispatches(subId, { page: 1, page_size: 50 })
      set((state) => ({
        dispatches: { ...state.dispatches, [subId]: { status: 'ready', data: result.items, error: null } },
      }))
    } catch (error) {
      const msg = error?.response?.data?.message || error?.message || 'Failed to load dispatches'
      set((state) => ({
        dispatches: { ...state.dispatches, [subId]: { status: 'error', data: [], error: msg } },
      }))
    }
  },

  createSubscription: async (sub) => {
    set({ submitting: true })
    try {
      const created = await eventRoutingService.createSubscription(sub)
      set((state) => ({
        subscriptions: { ...state.subscriptions, data: [created, ...state.subscriptions.data] },
        submitting: false,
      }))
      return created
    } catch (error) {
      set({ submitting: false })
      throw error
    }
  },

  updateSubscription: async (id, sub) => {
    set({ submitting: true })
    try {
      const updated = await eventRoutingService.updateSubscription(id, sub)
      set((state) => ({
        subscriptions: {
          ...state.subscriptions,
          data: state.subscriptions.data.map((s) => (s.id === id ? updated : s)),
        },
        submitting: false,
      }))
      return updated
    } catch (error) {
      set({ submitting: false })
      throw error
    }
  },

  deleteSubscription: async (id) => {
    await eventRoutingService.deleteSubscription(id)
    set((state) => ({
      subscriptions: {
        ...state.subscriptions,
        data: state.subscriptions.data.filter((s) => s.id !== id),
      },
    }))
  },

  togglePause: async (id) => {
    const sub = get().subscriptions.data.find((s) => s.id === id)
    if (!sub) return
    const nextStatus = sub.status === 'active' ? 'paused' : 'active'
    if (nextStatus === 'paused') {
      await eventRoutingService.pauseSubscription(id)
    } else {
      await eventRoutingService.resumeSubscription(id)
    }
    set((state) => ({
      subscriptions: {
        ...state.subscriptions,
        data: state.subscriptions.data.map((s) => (s.id === id ? { ...s, status: nextStatus } : s)),
      },
    }))
  },

  replayDispatch: async (subId, dispatchId) => {
    await eventRoutingService.replayDispatch(dispatchId)
    await get().fetchDispatches(subId)
  },

  reset: () => set(initialState),
}))

// ── Pure selectors (called inside components) ────────────────────────────

export function filterSubscriptions(list, q, statusFilter) {
  const ql = (q || '').toLowerCase()
  return (list || []).filter((s) => {
    const matchQ = !ql
      || s.name.toLowerCase().includes(ql)
      || s.eventTypes.some((e) => e.toLowerCase().includes(ql))
    const matchS = statusFilter === 'all' || s.status === statusFilter
    return matchQ && matchS
  })
}

export function filterCatalog(events, q, cat) {
  const ql = (q || '').toLowerCase()
  return (events || []).filter((e) => {
    const matchQ = !ql
      || e.name.toLowerCase().includes(ql)
      || e.description.toLowerCase().includes(ql)
    const matchC = cat === 'all' || e.category === cat
    return matchQ && matchC
  })
}
