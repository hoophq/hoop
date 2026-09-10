import { create } from 'zustand'
import { sidecarsService } from '@/services/sidecars'

// The fleet as the gateway lists it. createSidecar returns the response, token
// included, and keeps none of it: the token is shown once by the wizard that
// asked for it and must not outlive that page (compare useAgentStore.agentKey).
export const useSidecarStore = create((set) => ({
  sidecars: [],
  // True until the first fetch answers: an empty list is the onboarding screen,
  // and showing it before the list arrives tells every org it owns no sidecar.
  loading: true,
  error: null,

  fetchSidecars: async () => {
    set({ loading: true, error: null })
    try {
      const sidecars = await sidecarsService.list()
      set({ sidecars, loading: false })
    } catch (error) {
      set({ error: error.message, loading: false })
    }
  },

  createSidecar: async ({ name }) => {
    const created = await sidecarsService.create({ name })
    const { token: _token, ...sidecar } = created
    set((state) => ({ sidecars: [...state.sidecars, sidecar] }))
    return created
  },

  deleteSidecar: async (id) => {
    await sidecarsService.delete(id)
    set((state) => ({ sidecars: state.sidecars.filter((s) => s.id !== id) }))
  },
}))
