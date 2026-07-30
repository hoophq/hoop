import { create } from 'zustand'
import { reportsService } from '@/services/reports'
import { reviewsService } from '@/services/reviews'
import { sessionsService } from '@/services/sessions'
import { connectionsService } from '@/services/connections'
import { DEFAULT_RANGE } from './constants'
import { redactedRangeParams, todayReportParams, todaySessionParams } from './utils'

// Guards against out-of-order responses when the range selector is clicked
// faster than the report query returns: only the newest request may write.
let redactedRequestId = 0

const initialState = {
  loading: true,

  // "Today's overview". Null renders as an em dash — the value is unknown,
  // which is not the same as zero.
  todaySessionsTotal: null,
  todayRedactedTotal: null,
  sessionsError: null,
  todayRedactedError: null,

  // /reviews takes no query parameters, so the full list is fetched once and
  // both the today count and every chart range are derived from it.
  reviews: [],
  reviewsError: null,
  reviewRange: DEFAULT_RANGE,

  connections: [],
  connectionsError: null,

  redactedRange: DEFAULT_RANGE,
  redactedItems: [],
  redactedRangeLabel: '',
  redactedLoading: false,
  redactedError: null,
}

export const useDashboardStore = create((set, get) => ({
  ...initialState,

  /**
   * Loads everything the page needs. Each request writes its own slice, so one
   * failing endpoint degrades a single card instead of the whole dashboard —
   * /reviews in particular is unbounded and the most likely to time out.
   */
  fetchAll: async () => {
    set({
      loading: true,
      sessionsError: null,
      todayRedactedError: null,
      reviewsError: null,
      connectionsError: null,
    })

    const todayReport = todayReportParams()

    await Promise.all([
      sessionsService
        .list(todaySessionParams())
        .then((res) => set({ todaySessionsTotal: res.data?.total ?? 0 }))
        .catch(() => set({ sessionsError: 'Failed to load session count.' })),

      reportsService
        .getSessionReport(todayReport)
        .then((data) => set({ todayRedactedTotal: data?.total_redact_count ?? 0 }))
        .catch(() => set({ todayRedactedError: 'Failed to load redaction count.' })),

      reviewsService
        .list()
        .then((data) => set({ reviews: Array.isArray(data) ? data : [] }))
        .catch(() => set({ reviews: [], reviewsError: 'Failed to load reviews.' })),

      connectionsService
        .getConnections()
        .then((data) => set({ connections: Array.isArray(data) ? data : [] }))
        .catch(() => set({ connections: [], connectionsError: 'Failed to load resource roles.' })),

      get().fetchRedactedRange(get().redactedRange),
    ])

    set({ loading: false })
  },

  /** Redaction report for one range. Safe to call concurrently. */
  fetchRedactedRange: async (range) => {
    const requestId = ++redactedRequestId
    const { startDate, endDate, rangeLabel } = redactedRangeParams(Number(range))

    set({ redactedLoading: true, redactedError: null, redactedRangeLabel: rangeLabel })

    try {
      const data = await reportsService.getSessionReport({ startDate, endDate })
      if (requestId !== redactedRequestId) return
      set({ redactedItems: data?.items ?? [], redactedLoading: false })
    } catch {
      if (requestId !== redactedRequestId) return
      set({
        redactedItems: [],
        redactedLoading: false,
        redactedError: 'Failed to load redacted data.',
      })
    }
  },

  /** Client-side only — the reviews list is already fully loaded. */
  setReviewRange: (range) => set({ reviewRange: range }),

  setRedactedRange: (range) => {
    set({ redactedRange: range })
    get().fetchRedactedRange(range)
  },
}))
