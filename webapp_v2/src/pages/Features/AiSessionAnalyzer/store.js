import { create } from 'zustand'
import { accessRequestsService } from '@/services/accessRequests'
import { aiSessionAnalyzerService } from '@/services/aiSessionAnalyzer'

export const useAiSessionAnalyzerStore = create((set, get) => ({
  list: [],
  listStatus: 'idle', // 'idle' | 'loading' | 'success' | 'error'

  active: null,
  activeStatus: 'idle',

  provider: null,
  providerStatus: 'idle',

  systemPrompt: '',
  systemPromptStatus: 'idle',

  accessRequestRules: [],
  accessRequestRulesStatus: 'idle',

  submitting: false,

  fetchList: async () => {
    set({ listStatus: 'loading' })
    try {
      // Paginated envelope: { pages, data }.
      const { data } = await aiSessionAnalyzerService.listRules()
      set({ list: data?.data ?? [], listStatus: 'success' })
    } catch {
      set({ listStatus: 'error' })
    }
  },

  fetchActive: async (name) => {
    set({ active: null, activeStatus: 'loading' })
    try {
      const { data } = await aiSessionAnalyzerService.getRule(name)
      set({ active: data, activeStatus: 'success' })
    } catch {
      set({ activeStatus: 'error' })
    }
  },

  clearActive: () => set({ active: null, activeStatus: 'idle' }),

  // A 404 means "never configured", not a failure. Callers read
  // `Boolean(provider)`, never the status.
  fetchProvider: async () => {
    set({ providerStatus: 'loading' })
    try {
      const { data } = await aiSessionAnalyzerService.getProvider()
      set({ provider: data ?? null, providerStatus: 'success' })
    } catch (error) {
      if (error?.response?.status === 404) {
        set({ provider: null, providerStatus: 'success' })
        return
      }
      set({ providerStatus: 'error' })
    }
  },

  saveProvider: async (payload) => {
    set({ submitting: true })
    try {
      const { data } = await aiSessionAnalyzerService.saveProvider(payload)
      set({ provider: data ?? payload, submitting: false, providerStatus: 'success' })
      return { ok: true }
    } catch (error) {
      set({ submitting: false })
      return { ok: false, error }
    }
  },

  // The appended system prompt is a server-side constant, so it is fetched once
  // and reused for the rest of the session.
  fetchSystemPrompt: async () => {
    if (get().systemPrompt) return
    set({ systemPromptStatus: 'loading' })
    try {
      const { data } = await aiSessionAnalyzerService.getSystemPrompt()
      set({ systemPrompt: data?.prompt ?? '', systemPromptStatus: 'success' })
    } catch {
      set({ systemPromptStatus: 'error' })
    }
  },

  // Feeds the require_access_request picker in the rule form. The status is kept
  // so the form can tell "this org has no rules" from "the list failed to load".
  fetchAccessRequestRules: async () => {
    set({ accessRequestRulesStatus: 'loading' })
    try {
      const { data } = await accessRequestsService.list()
      set({
        accessRequestRules: data?.data ?? [],
        accessRequestRulesStatus: 'success',
      })
    } catch {
      set({ accessRequestRulesStatus: 'error' })
    }
  },

  createRule: async (payload) => {
    set({ submitting: true })
    try {
      await aiSessionAnalyzerService.createRule(payload)
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
      await aiSessionAnalyzerService.updateRule(name, payload)
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
      await aiSessionAnalyzerService.removeRule(name)
      set((state) => ({
        submitting: false,
        list: state.list.filter((rule) => rule.name !== name),
      }))
      return { ok: true }
    } catch (error) {
      set({ submitting: false })
      return { ok: false, error }
    }
  },
}))
