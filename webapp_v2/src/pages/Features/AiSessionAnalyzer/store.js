import { create } from 'zustand'
import { accessRequestsService } from '@/services/accessRequests'
import { aiSessionAnalyzerService } from '@/services/aiSessionAnalyzer'
import { dataMaskingService } from '@/services/dataMasking'
import { guardrailsService } from '@/services/guardrails'
import { useAuthStore } from '@/stores/useAuthStore'

const EMPTY_LIBRARY = { guardrails: [], mask: [] }

const isObject = (v) => v != null && typeof v === 'object' && !Array.isArray(v)

// A library row the AI may apply: its sidecar block holds at least one rule,
// each an object. The gateway accepts only these names (libraryGuardrails,
// libraryMasks in gateway/services/sidecarairules.go) and serves only these
// rows into rule_library; rulepack rows are not listed at all.
const holdsSidecarRules = (spec) =>
  isObject(spec) && Array.isArray(spec.rules) && spec.rules.length > 0 && spec.rules.every(isObject)

const libraryEntries = (rows) =>
  (Array.isArray(rows) ? rows : [])
    .filter((r) => holdsSidecarRules(r.sidecar_spec))
    .map((r) => ({ name: r.name, description: r.description ?? '' }))
    .sort((a, b) => a.name.localeCompare(b.name))

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

  // The guardrail and data masking rules a risk level may let the AI apply.
  // Session-scoped: cleared on logout. `ruleLibraryRequestId` is the
  // generation counter: only the newest fetch, of this session, commits.
  ruleLibrary: EMPTY_LIBRARY,
  ruleLibraryStatus: 'idle',
  ruleLibraryRequestId: 0,

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

  // Both lists or neither: a level shows its two features side by side, and
  // "all rules" is only written back when the list is known. The list is
  // emptied while a fetch runs and on a failure, so no earlier list (another
  // session's, or a stale one) is shown in its place.
  fetchRuleLibrary: async () => {
    const requestId = get().ruleLibraryRequestId + 1
    set({ ruleLibrary: EMPTY_LIBRARY, ruleLibraryStatus: 'loading', ruleLibraryRequestId: requestId })
    const current = () => get().ruleLibraryRequestId === requestId
    try {
      const [guardrails, mask] = await Promise.all([guardrailsService.list(), dataMaskingService.list()])
      if (!current()) return
      set({
        ruleLibrary: { guardrails: libraryEntries(guardrails.data), mask: libraryEntries(mask.data) },
        ruleLibraryStatus: 'success',
      })
    } catch {
      if (!current()) return
      set({ ruleLibrary: EMPTY_LIBRARY, ruleLibraryStatus: 'error' })
    }
  },

  // Bumps the generation, so a fetch still in flight cannot fill the list.
  resetRuleLibrary: () =>
    set((state) => ({
      ruleLibrary: EMPTY_LIBRARY,
      ruleLibraryStatus: 'idle',
      ruleLibraryRequestId: state.ruleLibraryRequestId + 1,
    })),

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

// The rule library names another org's rules: drop it when the session ends,
// as useSidecarStore does. Module state outlives a logout.
useAuthStore.subscribe((state, prev) => {
  if (prev.isAuthenticated && !state.isAuthenticated) {
    useAiSessionAnalyzerStore.getState().resetRuleLibrary()
  }
})
