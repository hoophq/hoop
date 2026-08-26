import { create } from 'zustand'
import { accessRequestsService } from '@/services/accessRequests'
import { attributesService } from '@/services/attributes'
import { userGroupsService } from '@/services/userGroups'

export const useAccessRequestStore = create((set) => ({
  rules: [],
  // 'idle' | 'loading' | 'success' | 'error'
  rulesStatus: 'idle',

  rule: null,
  ruleStatus: 'idle',

  attributes: [],
  attributesStatus: 'idle',

  userGroups: [],
  userGroupsStatus: 'idle',

  submitting: false,

  fetchRules: async () => {
    set({ rulesStatus: 'loading' })
    try {
      // Paginated envelope, even unpaginated: { pages, data }.
      const { data } = await accessRequestsService.list()
      set({ rules: data?.data ?? [], rulesStatus: 'success' })
    } catch {
      set({ rulesStatus: 'error' })
    }
  },

  fetchRule: async (name) => {
    set({ rule: null, ruleStatus: 'loading' })
    try {
      const { data } = await accessRequestsService.get(name)
      set({ rule: data, ruleStatus: 'success' })
    } catch {
      set({ ruleStatus: 'error' })
    }
  },

  clearRule: () => set({ rule: null, ruleStatus: 'idle' }),

  fetchAttributes: async () => {
    set({ attributesStatus: 'loading' })
    try {
      set({ attributes: await attributesService.listAll(), attributesStatus: 'success' })
    } catch {
      set({ attributesStatus: 'error' })
    }
  },

  fetchUserGroups: async () => {
    set({ userGroupsStatus: 'loading' })
    try {
      // Gateways older than EVL-217 answer `null` instead of `[]`.
      const { data } = await userGroupsService.list()
      set({ userGroups: data ?? [], userGroupsStatus: 'success' })
    } catch {
      set({ userGroupsStatus: 'error' })
    }
  },

  createRule: async (payload) => {
    set({ submitting: true })
    try {
      await accessRequestsService.create(payload)
      set({ submitting: false })
      return { ok: true }
    } catch (error) {
      set({ submitting: false })
      return { ok: false, error }
    }
  },

  updateRule: async (name, payload) => {
    set({ submitting: true })
    try {
      await accessRequestsService.update(name, payload)
      set({ submitting: false })
      return { ok: true }
    } catch (error) {
      set({ submitting: false })
      return { ok: false, error }
    }
  },

  deleteRule: async (name) => {
    set({ submitting: true })
    try {
      await accessRequestsService.remove(name)
      set((state) => ({
        submitting: false,
        rules: state.rules.filter((rule) => rule.name !== name),
      }))
      return { ok: true }
    } catch (error) {
      set({ submitting: false })
      return { ok: false, error }
    }
  },
}))
