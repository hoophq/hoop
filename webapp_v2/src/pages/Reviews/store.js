import { create } from 'zustand'
import { reviewsService } from '@/services/reviews'
import { sessionsService } from '@/services/sessions'

export const useReviewStore = create((set, get) => ({
  reviews: [],
  // 'idle' | 'loading' | 'success' | 'error'
  reviewsStatus: 'idle',

  // Keyed by session id: the statement the sidecar held, fetched per drawer.
  statements: {},
  statementStatus: {},

  submitting: false,

  fetchReviews: async () => {
    set({ reviewsStatus: 'loading' })
    try {
      const { data } = await reviewsService.list()
      set({ reviews: Array.isArray(data) ? data : [], reviewsStatus: 'success' })
    } catch {
      set({ reviewsStatus: 'error' })
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
      set((s) => ({
        submitting: false,
        reviews: s.reviews.map((review) => (review.id === data.id ? data : review)),
      }))
      return { ok: true, review: data }
    } catch (error) {
      set({ submitting: false })
      return { ok: false, error }
    }
  },
}))
