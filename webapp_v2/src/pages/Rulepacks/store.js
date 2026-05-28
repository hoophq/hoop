import { create } from 'zustand'
import { rulepacksService } from '@/services/rulepacks'
import { connectionsService } from '@/services/connections'

const arraysEqualAsSets = (a, b) => {
  if (a.size !== b.size) return false
  for (const v of a) if (!b.has(v)) return false
  return true
}

export const useRulepackStore = create((set, get) => ({
  list: [],
  listStatus: 'idle',
  listSearch: '',

  active: null,
  activeStatus: 'idle',

  selectedConnections: new Set(),
  applying: false,

  connections: [],
  connectionsStatus: 'idle',

  fetchList: async (search) => {
    const q = (search ?? '').trim()
    set({ listStatus: 'loading', listSearch: q })
    try {
      const { data } = await rulepacksService.list(q)
      set({ list: data?.data ?? [], listStatus: 'success' })
    } catch {
      set({ listStatus: 'error' })
    }
  },

  fetchActive: async (rulepackId) => {
    set({
      activeStatus: 'loading',
      active: null,
      selectedConnections: new Set(),
    })
    try {
      const { data } = await rulepacksService.get(rulepackId)
      set({
        active: data,
        activeStatus: 'success',
        selectedConnections: new Set(data?.connection_names ?? []),
      })
    } catch {
      set({ activeStatus: 'error' })
    }
  },

  toggleConnection: (connectionName) =>
    set((state) => {
      const next = new Set(state.selectedConnections)
      if (next.has(connectionName)) next.delete(connectionName)
      else next.add(connectionName)
      return { selectedConnections: next }
    }),

  resetSelectedConnections: () =>
    set((state) => ({
      selectedConnections: new Set(state.active?.connection_names ?? []),
    })),

  applyConnections: async () => {
    const { active, selectedConnections } = get()
    if (!active?.id) return { ok: false }
    set({ applying: true })
    try {
      await rulepacksService.apply(active.id, Array.from(selectedConnections))
      set({ applying: false })
      await get().fetchActive(active.id)
      return { ok: true }
    } catch (error) {
      set({ applying: false })
      return {
        ok: false,
        missing: error?.response?.data?.missing_names ?? [],
      }
    }
  },

  hasPendingChanges: () => {
    const { active, selectedConnections } = get()
    const saved = new Set(active?.connection_names ?? [])
    return !arraysEqualAsSets(saved, selectedConnections)
  },

  fetchConnections: async () => {
    set({ connectionsStatus: 'loading' })
    try {
      const data = await connectionsService.getConnections()
      set({ connections: data ?? [], connectionsStatus: 'success' })
    } catch {
      set({ connectionsStatus: 'error' })
    }
  },
}))
