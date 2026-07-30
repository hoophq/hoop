import api from './api'

export const reportsService = {
  /**
   * Session redaction report.
   *
   * `startDate` / `endDate` must be `YYYY-MM-DD` — the gateway parses them with
   * `time.DateOnly` and rejects RFC3339 with a 400. The range is capped at 120
   * days server-side, and it filters on `ended_at`, so sessions still running
   * are excluded.
   *
   * `end_date` is exclusive in practice: the SQL compares against
   * `TO_TIMESTAMP(end_date, 'YYYY-MM-DD')`, i.e. midnight *starting* that day.
   * Pass tomorrow to include today.
   *
   * We deliberately never send `group_by`. The handler only accepts
   * `connection | id | user_email | connection_type`, but the SQL switches on
   * `connection_name` — so the documented default yields an empty `resource` on
   * every row, and the handler's own internal default is rejected with a 400.
   * Omitting it takes the working path.
   *
   * → { items: [{ resource, info_type, redact_total, transformed_bytes }],
   *     total_redact_count, total_transformed_bytes }
   */
  getSessionReport: ({ startDate, endDate } = {}) => {
    const params = new URLSearchParams()
    if (startDate) params.set('start_date', startDate)
    if (endDate) params.set('end_date', endDate)
    return api.get(`/reports/sessions?${params.toString()}`).then((res) => res.data)
  },
}
