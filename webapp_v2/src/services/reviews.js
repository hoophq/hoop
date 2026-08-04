import api from './api'

export const reviewsService = {
  /**
   * Every review in the organization, as a bare JSON array.
   *
   * The endpoint accepts NO query parameters — no date filter, no status
   * filter, no pagination — so callers must filter client-side, and there is no
   * point re-requesting when a date range changes. Fetch once, derive many.
   *
   * → [{ id, session, type, status, created_at, review_groups_data, ... }]
   *   status ∈ PENDING | APPROVED | REJECTED | REVOKED | PROCESSING | EXECUTED | UNKNOWN
   */
  list: () => api.get('/reviews').then((res) => res.data),

  /**
   * Port of :audit->add-review (webapp events/audit.cljs:534-568).
   *
   * `status` is upper-cased server-side expectations: APPROVED | REJECTED.
   * Times go over the wire in **UTC** (`local-time->utc-time` in v1) and come
   * back rendered through the inverse, so callers must convert before calling.
   *
   *   addReview(reviewId, { status, timeWindow: { startTime, endTime },
   *                         forceReview, rejectionReason })
   */
  addReview: (reviewId, { status, timeWindow, forceReview, rejectionReason } = {}) => {
    const body = { status: String(status).toUpperCase() }
    if (timeWindow?.startTime && timeWindow?.endTime) {
      body.time_window = {
        type: 'time_range',
        configuration: {
          start_time: timeWindow.startTime,
          end_time: timeWindow.endTime,
        },
      }
    }
    if (forceReview) body.force_review = true
    if (rejectionReason && rejectionReason.trim() !== '') {
      body.rejection_reason = rejectionReason
    }
    return api.put(`/reviews/${encodeURIComponent(reviewId)}`, body).then((r) => r.data)
  },
}
