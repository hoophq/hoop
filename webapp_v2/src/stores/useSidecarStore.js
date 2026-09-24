import { create } from 'zustand'
import { sidecarsService } from '@/services/sidecars'
import { useAuthStore } from '@/stores/useAuthStore'

const EMPTY = {
  sidecars: [],
  // True until the first fetch answers: an empty list is the onboarding screen,
  // and showing it before the list arrives tells every org it owns no sidecar.
  loading: true,
  error: null,
  // The one record /sidecars/:id is looking at, for as long as that page is
  // mounted. `selectedId` is what the store went to fetch, so a page compares
  // it with the route to know whether the record on screen is its own;
  // `selectedLoading` says whether that fetch has answered yet. Both are
  // needed: the first alone reads "we asked for this id", which is true from
  // the moment the request leaves.
  selected: null,
  selectedId: null,
  selectedError: null,
  selectedLoading: false,
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
      set((state) =>
        state.listRequestId === requestId ? { sidecars: data ?? [], loading: false } : { loading: false },
      )
    } catch (error) {
      set((state) =>
        state.listRequestId === requestId ? { error: error.message, loading: false } : { loading: false },
      )
    }
  },

  // Clears the previous record before the request leaves, so nothing can render
  // one sidecar under another's URL.
  //
  // A superseded response leaves `selectedLoading` alone, unlike the list: the
  // generation only changes when a newer fetch or clearSelected took over, and
  // that caller owns the flag now.
  fetchSidecar: async (nameOrId) => {
    const requestId = get().selectedRequestId + 1
    set({
      selectedRequestId: requestId,
      selectedId: nameOrId,
      selected: null,
      selectedError: null,
      selectedLoading: true,
    })
    try {
      const { data } = await sidecarsService.get(nameOrId)
      set((state) => (state.selectedRequestId === requestId ? { selected: data, selectedLoading: false } : {}))
    } catch (error) {
      const message = error.response?.status === 404 ? 'Sidecar not found.' : error.message
      set((state) => (state.selectedRequestId === requestId ? { selectedError: message, selectedLoading: false } : {}))
    }
  },

  // Re-read one sidecar in place, with no loading state, so a dialog that
  // opens on it counts what the gateway holds now.
  refreshSidecar: async (id) => {
    const { data } = await sidecarsService.get(id)
    set((state) => ({
      sidecars: state.sidecars.map((s) => (s.id === data.id ? data : s)),
      selected: state.selected?.id === data.id ? data : state.selected,
    }))
    return data
  },

  // The selected record belongs to one page view, not to the app: a details
  // page that unmounts drops it. Keeping it would let the next visit to the
  // same URL paint a record minutes old — or one already deleted — before the
  // refresh lands, with nothing on screen saying it is stale. Bumping the
  // generation also retires a request still in flight.
  clearSelected: () =>
    set((state) => ({
      selected: null,
      selectedId: null,
      selectedError: null,
      selectedLoading: false,
      selectedRequestId: state.selectedRequestId + 1,
    })),

  createSidecar: async ({ name }) => {
    const { data } = await sidecarsService.create({ name })
    const { token: _token, ...sidecar } = data
    set((state) => ({ sidecars: [...state.sidecars, sidecar], listRequestId: state.listRequestId + 1 }))
    return data
  },

  // Flip which side owns this sidecar's configuration.
  //
  // One atomic PATCH merges only `load_from_disk`. To the config file, the
  // gateway deletes the rules imported from this sidecar that nothing else
  // uses and unbinds the rest; `detached_rules` lists them. Back to the
  // control plane, the stored document is emptied and the sidecar imports its
  // file again.
  setUsesConfigFile: async (id, usesConfigFile) => {
    const { data: updated } = await sidecarsService.patch(id, { load_from_disk: usesConfigFile })
    set((state) => ({
      sidecars: state.sidecars.map((s) => (s.id === updated.id ? updated : s)),
      // Only the record on screen: a flip still in flight when the route
      // moves on must not write the previous sidecar back over the next.
      selected: state.selected?.id === updated.id ? updated : state.selected,
      listRequestId: state.listRequestId + 1,
    }))
    return updated
  },

  /**
   * Replace a sidecar's whole configuration document.
   *
   * Returns `{ ok, error }` rather than throwing, so a form can put the
   * gateway's message next to the field it is about. The rest of this store
   * still throws; the wizard it serves has no field to put a message in.
   *
   * The updated sidecar is merged back into the list, and into `selected` when
   * it is the record on screen, so both show the new listeners without a
   * refetch and neither can serve a stale document to the next write.
   */
  updateSidecar: async (nameOrId, configuration) => {
    try {
      const { data: updated } = await sidecarsService.update(nameOrId, configuration)
      set((state) => ({
        sidecars: state.sidecars.map((s) => (s.id === updated.id ? updated : s)),
        // `selected` too, and this is the one that bites: the details page
        // deletes a listener WITHOUT navigating, and builds the next
        // whole-document PUT from selected.configuration. Leaving it stale
        // means a second delete writes the first listener back.
        selected: state.selected?.id === updated.id ? updated : state.selected,
        // Both counters, because this is a write that replaces what a read
        // would return. `requestId` was neither of them and existed nowhere:
        // state.requestId is undefined, so the old line stored NaN.
        listRequestId: state.listRequestId + 1,
        selectedRequestId: state.selectedRequestId + 1,
      }))
      return { ok: true, sidecar: updated }
    } catch (error) {
      return { ok: false, error }
    }
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
