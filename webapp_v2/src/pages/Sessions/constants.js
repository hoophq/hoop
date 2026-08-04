export const PAGE_SIZE = 20

/** Guard on the comma-separated ID search box: each id costs one GET. */
export const MAX_LOOKUP_IDS = 50

/** audit_filters.cljs:24-26 — a static list in v1 too, not an API call. */
export const TYPE_OPTIONS = ['custom', 'database', 'application']

/**
 * audit_filters.cljs:45-47. The gateway's ReviewStatusType also has REVOKED,
 * PROCESSING, EXECUTED and UNKNOWN, but v1 only ever offered these three as
 * filters — the others are display-only (see ReviewStatusBadge).
 */
export const REVIEW_STATUS_OPTIONS = ['PENDING', 'APPROVED', 'REJECTED']

export const SESSIONS_DOCS_URL =
  'https://hoop.dev/docs/learn/features/session-recording'
