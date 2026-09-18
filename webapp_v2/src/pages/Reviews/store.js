import { create } from 'zustand'
import { reviewsService } from '@/services/reviews'
import { sessionsService } from '@/services/sessions'
import { useAuthStore } from '@/stores/useAuthStore'

const EMPTY = {
  reviews: [],
  // 'idle' | 'loading' | 'success' | 'error'
  reviewsStatus: 'idle',
  // Keyed by session id: the statement the sidecar held, fetched per modal.
  statements: {},
  statementStatus: {},
  submitting: false,
}

export const useReviewStore = create((set, get) => ({
  ...EMPTY,
  // Bumped by every list fetch, every decision and the logout reset, so a
  // response that arrives after the list moved on is dropped instead of
  // restoring what it saw.
  listRequestId: 0,

  reset: () => set((state) => ({ ...EMPTY, listRequestId: state.listRequestId + 1 })),

  fetchReviews: async () => {
    const requestId = get().listRequestId + 1
    set({ listRequestId: requestId, reviewsStatus: 'loading' })
    try {
      const { data } = await reviewsService.list()
      set((state) =>
        state.listRequestId === requestId
          ? { reviews: Array.isArray(data) ? data : [], reviewsStatus: 'success' }
          : {},
      )
    } catch {
      set((state) => (state.listRequestId === requestId ? { reviewsStatus: 'error' } : {}))
    }
  },

  fetchStatement: async (sessionId) => {
    if (!sessionId || get().statementStatus[sessionId] === 'loading') return
    set((s) => ({ statementStatus: { ...s.statementStatus, [sessionId]: 'loading' } }))
    try {
      const { data } = await sessionsService.get(sessionId)
      set((s) => ({
        statements: { ...s.statements, [sessionId]: data?.script?.data ?? '' },
        statementStatus: { ...s.statementStatus, [sessionId]: 'success' },
      }))
    } catch {
      set((s) => ({ statementStatus: { ...s.statementStatus, [sessionId]: 'error' } }))
    }
  },

  decide: async (id, payload) => {
    set({ submitting: true })
    try {
      const { data } = await reviewsService.update(id, payload)
      set((state) => ({
        submitting: false,
        // Also bumped so a list fetch still in flight cannot restore the row
        // as it was before this decision.
        listRequestId: state.listRequestId + 1,
        reviews: state.reviews.map((review) => (review.id === data.id ? data : review)),
      }))
      return { ok: true, review: data }
    } catch (error) {
      set({ submitting: false })
      return { ok: false, error }
    }
  },
}))

// Module state outlives a logout, so the next user in the same tab would see
// the previous organization's reviews and statements until the first fetch
// answers. Subscribed from this side, as useSidecarStore does.
useAuthStore.subscribe((state, prev) => {
  if (prev.isAuthenticated && !state.isAuthenticated) {
    useReviewStore.getState().reset()
  }
})
