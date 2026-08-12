import { create } from 'zustand'
import { onboardingService } from '@/services/onboarding'
import { useAuthStore } from '@/stores/useAuthStore'
import { useUserStore } from '@/stores/useUserStore'

// EVL-98 / DEP-136: backs the sidebar Config Status checklist.
//
// The gateway owns it: GET /orgs/onboarding answers every item in a single
// query, and /userinfo's `show_setup_checklist` decides whether the widget
// exists at all. The first version derived the checklist in the browser from
// eight endpoints on every route change, on focus and on two timers — one of
// them GET /sessions, whose unbounded COUNT took ~2min at 1.5M sessions and
// took a customer database down. Do not reintroduce client-side derivation.

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

    // A stale snapshot from a previous login (same tab) must never satisfy the
    // TTL or the completion short-circuit — refetch whenever the user changed.
    const { lastFetchedAt, forUserId, completed } = get()
    const sameUser = forUserId === userId
    // Completion is terminal server side: nothing left to poll for, and the
    // widget hides without waiting for the next /userinfo.
    if (sameUser && completed) return
    if (!force && sameUser && lastFetchedAt && Date.now() - lastFetchedAt < TTL_MS) return

    if (inFlight) {
      if (inFlightUserId === userId) return inFlight
      // A different user's fetch is in flight — wait it out, then re-evaluate
      // from scratch (TTL, user match, and any fetch started meanwhile).
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

// This is module state, so it outlives a logout: nothing on that path reloads
// the tab (the header menu just calls logout() and navigates). Without this the
// next user in the same tab inherits the previous org's `completed`, and since
// the widget is gated on it, it never mounts to correct itself.
//
// Subscribing from this side rather than calling reset() from useAuthStore keeps
// the dependency pointing the right way, same as useNativeAccessStore.
useAuthStore.subscribe((state, prev) => {
  if (prev.isAuthenticated && !state.isAuthenticated) {
    useConfigStatusStore.getState().reset()
  }
})
