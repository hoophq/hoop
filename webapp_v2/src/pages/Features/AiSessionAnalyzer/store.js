import { create } from 'zustand'
import { accessRequestsService } from '@/services/accessRequests'
import { aiSessionAnalyzerService } from '@/services/aiSessionAnalyzer'
import { attributesService } from '@/services/attributes'
import { dataMaskingService } from '@/services/dataMasking'
import { guardrailsService } from '@/services/guardrails'
import { reviewsService } from '@/services/reviews'
import { useAuthStore } from '@/stores/useAuthStore'

const EMPTY_LIBRARY = { guardrails: [], mask: [] }

// The integrations an AI-routed approval notifies through (ENG-663).
const EMPTY_INTEGRATION = { integrated: false, channels: [] }
const EMPTY_INTEGRATIONS = { slack: EMPTY_INTEGRATION, teams: EMPTY_INTEGRATION }

// One integration of GET /approvals/routing/integrations. A channel with no
// id cannot be named in the spec, so it is dropped.
const readIntegration = (value) => ({
  integrated: value?.integrated === true,
  channels: (Array.isArray(value?.channels) ? value.channels : [])
    .filter((c) => typeof c?.id === 'string' && c.id !== '')
    .map((c) => ({ id: c.id, name: typeof c.name === 'string' && c.name !== '' ? c.name : c.id })),
})

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
  accessRequestRulesRequestId: 0,

  // The guardrail and data masking rules a risk level may let the AI apply.
  // Session-scoped: cleared on logout. `ruleLibraryRequestId` is the
  // generation counter: only the newest fetch, of this session, commits.
  ruleLibrary: EMPTY_LIBRARY,
  ruleLibraryStatus: 'idle',
  ruleLibraryRequestId: 0,

  // Session-scoped as the rule library: cleared on logout, and only the
  // newest fetch of this session commits.
  routingIntegrations: EMPTY_INTEGRATIONS,
  routingIntegrationsStatus: 'idle',
  routingIntegrationsRequestId: 0,

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

  // Emptied while a fetch runs and on a failure, so no other session's
  // channels are shown.
  fetchRoutingIntegrations: async () => {
    const requestId = get().routingIntegrationsRequestId + 1
    set({
      routingIntegrations: EMPTY_INTEGRATIONS,
      routingIntegrationsStatus: 'loading',
      routingIntegrationsRequestId: requestId,
    })
    const current = () => get().routingIntegrationsRequestId === requestId
    try {
      const { data } = await reviewsService.routingIntegrations()
      if (!current()) return
      set({
        routingIntegrations: { slack: readIntegration(data?.slack), teams: readIntegration(data?.teams) },
        routingIntegrationsStatus: 'success',
      })
    } catch {
      if (!current()) return
      set({ routingIntegrations: EMPTY_INTEGRATIONS, routingIntegrationsStatus: 'error' })
    }
  },

  resetRoutingIntegrations: () =>
    set((state) => ({
      routingIntegrations: EMPTY_INTEGRATIONS,
      routingIntegrationsStatus: 'idle',
      routingIntegrationsRequestId: state.routingIntegrationsRequestId + 1,
    })),

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

// Logout keeps the tab, and module state outlives it. The rule library, the
// integration channels, the access request rules and the attributes belong to
// the account: drop them before the next account opens the form.
useAuthStore.subscribe((state, prev) => {
  if (prev.isAuthenticated && !state.isAuthenticated) {
    useAiSessionAnalyzerStore.getState().resetRuleLibrary()
    useAiSessionAnalyzerStore.getState().resetRoutingIntegrations()
    useAiSessionAnalyzerStore.setState({
      accessRequestRules: [],
      accessRequestRulesStatus: 'idle',
      attributes: [],
      attributesStatus: 'idle',
    })
  }
})
