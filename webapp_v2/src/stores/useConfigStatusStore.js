import { create } from 'zustand'
import { onboardingService } from '@/services/onboarding'
import { useAuthStore } from '@/stores/useAuthStore'
import { useUserStore } from '@/stores/useUserStore'

// EVL-98 / DEP-136: backs the sidebar Config Status checklist.
//
// The gateway owns it: GET /orgs/onboarding answers every item in one query.
// This used to derive the checklist from eight endpoints on every route change,
// on focus and on two timers — one of them GET /sessions, whose unbounded COUNT
// took ~2min at 1.5M sessions. Do not reintroduce client-side derivation.

// Dedupes the navigation and focus triggers.
const TTL_MS = 15_000

const INITIAL_CHECKS = {
  agent_deployed: false,
  resource_created: false,
  session_ran: false,
  groups_created: false,
  people_assigned: false,
  guardrails_explored: false,
  data_masking_explored: false,
  ai_analyzer_enabled: false,
  protection_level_set: false,
}

const INITIAL_STATE = {
  status: 'idle', // 'idle' | 'loading' | 'ready'
  completed: false, // server-latched; once true the widget is done for good
  checks: { ...INITIAL_CHECKS },
  execConnectionName: null, // first exec-capable connection, for "Run your first session"
  firstConnectionName: null, // fallback when no connection allows exec
  lastFetchedAt: null,
  forUserId: null, // whose /userinfo the snapshot was computed against
}

let inFlight = null
let inFlightUserId = null

export const useConfigStatusStore = create((set, get) => ({
  ...INITIAL_STATE,

  fetchStatus: async ({ force = false } = {}) => {
    const { isAdmin, user } = useUserStore.getState()
    if (!isAdmin || !user?.show_setup_checklist) return
    const userId = user?.id ?? null

    // A snapshot from a previous login must not satisfy the TTL or the
    // completion short-circuit, so both are gated on the user matching.
    const { lastFetchedAt, forUserId, completed } = get()
    const sameUser = forUserId === userId
    // Completion is terminal server side: nothing left to fetch.
    if (sameUser && completed) return
    if (!force && sameUser && lastFetchedAt && Date.now() - lastFetchedAt < TTL_MS) return

    if (inFlight) {
      if (inFlightUserId === userId) return inFlight
      // Another user's fetch is in flight — wait it out, then re-evaluate.
      await inFlight.catch(() => {})
      return get().fetchStatus({ force })
    }

    inFlightUserId = userId
    inFlight = (async () => {
      set((state) => ({ status: state.status === 'ready' ? 'ready' : 'loading' }))

      let data
      try {
        data = await onboardingService.get()
      } catch (err) {
        // Keep the previous state; the next trigger retries past the TTL.
        console.warn('[useConfigStatusStore] failed loading onboarding status:', err)
        return
      }

      set({
        completed: !!data?.completed,
        checks: { ...INITIAL_CHECKS, ...data?.checks },
        execConnectionName: data?.exec_connection_name ?? null,
        firstConnectionName: data?.first_connection_name ?? null,
        status: 'ready',
        lastFetchedAt: Date.now(),
        forUserId: userId,
      })
    })()

    try {
      await inFlight
    } finally {
      inFlight = null
      inFlightUserId = null
    }
  },

  reset: () => {
    inFlight = null
    inFlightUserId = null
    set({ ...INITIAL_STATE, checks: { ...INITIAL_CHECKS } })
  },
}))

// Module state outlives a logout — nothing on that path reloads the tab. The
// next user would inherit the previous org's `completed`, and the widget is
// gated on it, so it would never mount to correct itself. Subscribing from this
// side keeps the dependency pointing the right way, like useNativeAccessStore.
useAuthStore.subscribe((state, prev) => {
  if (prev.isAuthenticated && !state.isAuthenticated) {
    useConfigStatusStore.getState().reset()
  }
})
