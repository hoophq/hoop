import { create } from 'zustand'
import { sidecarsService } from '@/services/sidecars'
import { useAuthStore } from '@/stores/useAuthStore'

const EMPTY = {
  sidecars: [],
  // True until the first fetch answers: an empty list is the onboarding screen,
  // and showing it before the list arrives tells every org it owns no sidecar.
  loading: true,
  error: null,
}

// The fleet as the gateway lists it. createSidecar returns the response, token
// included, and keeps none of it: the token is shown once by the wizard that
// asked for it and must not outlive that page (compare useAgentStore.agentKey).
export const useSidecarStore = create((set, get) => ({
  ...EMPTY,
  // Bumped by every fetch, every mutation and the logout reset, so a list
  // response that arrives after the fleet moved on is dropped rather than
  // restoring what it saw.
  requestId: 0,

  reset: () => set((state) => ({ ...EMPTY, requestId: state.requestId + 1 })),

  fetchSidecars: async () => {
    const requestId = get().requestId + 1
    set({ requestId, loading: true, error: null })
    // A superseded response still clears `loading` — the request it belonged to
    // is over either way, and leaving it set strands the page on a loader.
    try {
      const sidecars = await sidecarsService.list()
      set((state) => (state.requestId === requestId ? { sidecars, loading: false } : { loading: false }))
    } catch (error) {
      set((state) => (state.requestId === requestId ? { error: error.message, loading: false } : { loading: false }))
    }
  },

  createSidecar: async ({ name }) => {
    const created = await sidecarsService.create({ name })
    const { token: _token, ...sidecar } = created
    set((state) => ({ sidecars: [...state.sidecars, sidecar], requestId: state.requestId + 1 }))
    return created
  },

  deleteSidecar: async (id) => {
    await sidecarsService.delete(id)
    set((state) => ({ sidecars: state.sidecars.filter((s) => s.id !== id), requestId: state.requestId + 1 }))
  },
}))

/**
 * Drop the fleet the moment the session ends.
 *
 * This is module state, so it outlives a logout: nothing on that path reloads
 * the tab (the header menu calls logout() and navigates), which would leave the
 * next user in the same tab looking at the previous org's sidecars until the
 * first fetch answers.
 *
 * Subscribing from this side rather than calling reset() from useAuthStore keeps
 * the dependency pointing the right way, as useNativeAccessStore does.
 */
useAuthStore.subscribe((state, prev) => {
  if (prev.isAuthenticated && !state.isAuthenticated) {
    useSidecarStore.getState().reset()
  }
})
