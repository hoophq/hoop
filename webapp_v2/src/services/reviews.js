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
}
