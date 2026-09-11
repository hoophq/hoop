import { create } from 'zustand'
import { sidecarsService } from '@/services/sidecars'
import { useAuthStore } from '@/stores/useAuthStore'

const EMPTY = {
  sidecars: [],
  // True until the first fetch answers: an empty list is the onboarding screen,
  // and showing it before the list arrives tells every org it owns no sidecar.
  loading: true,
  error: null,
  // The one record /sidecars/:id is looking at. `selectedId` is what the store
  // went to fetch, not what the route asks for, so a page compares the two to
  // know whether the record on screen is its own.
  selected: null,
  selectedId: null,
  selectedError: null,
}

// The fleet as the gateway lists it, and the single record the details page
// reads. createSidecar returns the response, token included, and keeps none of
// it: the token is shown once by the wizard that asked for it and must not
// outlive that page (compare useAgentStore.agentKey).
export const useSidecarStore = create((set, get) => ({
  ...EMPTY,
  // One generation per resource. The list and the selected record are fetched
  // independently, so a shared counter would let either cancel the other.
  // Bumped by every fetch, every mutation and the logout reset, so a response
  // that arrives after its resource moved on is dropped rather than restoring
  // what it saw.
  listRequestId: 0,
  selectedRequestId: 0,

  reset: () =>
    set((state) => ({
      ...EMPTY,
      listRequestId: state.listRequestId + 1,
      selectedRequestId: state.selectedRequestId + 1,
    })),

  fetchSidecars: async () => {
    const requestId = get().listRequestId + 1
    set({ listRequestId: requestId, loading: true, error: null })
    // A superseded response still clears `loading` — the request it belonged to
    // is over either way, and leaving it set strands the page on a loader.
    try {
      const { data } = await sidecarsService.list()
      set((state) => (state.listRequestId === requestId ? { sidecars: data ?? [], loading: false } : { loading: false }))
    } catch (error) {
      set((state) =>
        state.listRequestId === requestId ? { error: error.message, loading: false } : { loading: false }
      )
    }
  },

  // Clears the previous record before the request leaves, so nothing can render
  // one sidecar under another's URL.
  fetchSidecar: async (nameOrId) => {
    const requestId = get().selectedRequestId + 1
    set({ selectedRequestId: requestId, selectedId: nameOrId, selected: null, selectedError: null })
    try {
      const { data } = await sidecarsService.get(nameOrId)
      set((state) => (state.selectedRequestId === requestId ? { selected: data } : {}))
    } catch (error) {
      const message = error.response?.status === 404 ? 'Sidecar not found.' : error.message
      set((state) => (state.selectedRequestId === requestId ? { selectedError: message } : {}))
    }
  },

  createSidecar: async ({ name }) => {
    const { data } = await sidecarsService.create({ name })
    const { token: _token, ...sidecar } = data
    set((state) => ({ sidecars: [...state.sidecars, sidecar], listRequestId: state.listRequestId + 1 }))
    return data
  },

  deleteSidecar: async (id) => {
    await sidecarsService.delete(id)
    set((state) => ({
      sidecars: state.sidecars.filter((s) => s.id !== id),
      listRequestId: state.listRequestId + 1,
    }))
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
