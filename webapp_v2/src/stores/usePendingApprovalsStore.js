import { create } from 'zustand'
import { reviewsService } from '@/services/reviews'
import { useAuthStore } from '@/stores/useAuthStore'
import { useUIStore } from '@/stores/useUIStore'

export const usePendingApprovalsStore = create((set, get) => ({
  count: 0,
  // Bumped by every fetch and by the logout reset, so a late response cannot
  // restore a count the store moved past.
  requestId: 0,

  fetchCount: async () => {
    // The collapsed sidebar shows no count, so it reads none.
    if (useUIStore.getState().sidebarCollapsed) return
    const requestId = get().requestId + 1
    set({ requestId })
    try {
      const { data } = await reviewsService.count('PENDING')
      set((state) => (state.requestId === requestId ? { count: data?.count ?? 0 } : {}))
    } catch {
      // Keeps the last count; the next navigation or focus reads it again.
    }
  },

  reset: () => set((state) => ({ count: 0, requestId: state.requestId + 1 })),
}))

useAuthStore.subscribe((state, prev) => {
  if (prev.isAuthenticated && !state.isAuthenticated) {
    usePendingApprovalsStore.getState().reset()
  }
})
