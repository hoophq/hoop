import { create } from 'zustand'
import { accessRequestsService } from '@/services/accessRequests'
import { aiSessionAnalyzerService } from '@/services/aiSessionAnalyzer'
import { attributesService } from '@/services/attributes'
import { useAuthStore } from '@/stores/useAuthStore'

export const useAiSessionAnalyzerStore = create((set, get) => ({
  list: [],
  listStatus: 'idle', // 'idle' | 'loading' | 'success' | 'error'

  active: null,
  activeStatus: 'idle',
  activeRequestedName: null,

  provider: null,
  providerStatus: 'idle',

  systemPrompt: '',
  systemPromptStatus: 'idle',

  accessRequestRules: [],
  accessRequestRulesStatus: 'idle',
  accessRequestRulesRequestId: 0,

  attributes: [],
  attributesStatus: 'idle',
  attributesRequestId: 0,

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

  // Committing a response for a rule that is no longer the one being edited
  // would pair its values with another rule's update path.
  fetchActive: async (name) => {
    set({ active: null, activeStatus: 'loading', activeRequestedName: name })
    try {
      const { data } = await aiSessionAnalyzerService.getRule(name)
      if (get().activeRequestedName !== name) return
      set({ active: data, activeStatus: 'success' })
    } catch {
      if (get().activeRequestedName !== name) return
      set({ activeStatus: 'error' })
    }
  },

  clearActive: () =>
    set({ active: null, activeStatus: 'idle', activeRequestedName: null }),

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
    // Cleared at the start so the form never reads a list from an earlier visit
    // or account; the id drops a response that a newer fetch replaced.
    const requestId = get().accessRequestRulesRequestId + 1
    set({
      accessRequestRules: [],
      accessRequestRulesStatus: 'loading',
      accessRequestRulesRequestId: requestId,
    })
    try {
      const { data } = await accessRequestsService.list()
      if (get().accessRequestRulesRequestId !== requestId) return
      set({
        accessRequestRules: data?.data ?? [],
        accessRequestRulesStatus: 'success',
      })
    } catch {
      if (get().accessRequestRulesRequestId !== requestId) return
      set({ accessRequestRulesStatus: 'error' })
    }
  },

  // With the access request rules, tells the form which selected roles an
  // access request rule also covers through a shared attribute.
  fetchAttributes: async () => {
    const requestId = get().attributesRequestId + 1
    set({ attributes: [], attributesStatus: 'loading', attributesRequestId: requestId })
    try {
      const attributes = await attributesService.listAll()
      if (get().attributesRequestId !== requestId) return
      set({ attributes, attributesStatus: 'success' })
    } catch {
      if (get().attributesRequestId !== requestId) return
      set({ attributesStatus: 'error' })
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

// The access request rules and attributes belong to the account. Logout keeps
// the tab, so they are dropped here, before the next account opens the form.
useAuthStore.subscribe((state, prev) => {
  if (prev.isAuthenticated && !state.isAuthenticated) {
    useAiSessionAnalyzerStore.setState({
      accessRequestRules: [],
      accessRequestRulesStatus: 'idle',
      attributes: [],
      attributesStatus: 'idle',
    })
  }
})
