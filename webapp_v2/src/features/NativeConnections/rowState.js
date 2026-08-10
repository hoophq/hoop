import { hasLiveSession, isPendingReviewFor } from './helpers'

export const ROW_STATE = {
  IDLE: 'idle',
  ACTIVE_BOUNDED: 'activeBounded',
  ACTIVE_PERSISTENT: 'activePersistent',
  NEEDS_REVIEW: 'needsReview',
  PENDING_REVIEW: 'pendingReview',
  OFFLINE: 'offline',
  ACCESS_REVOKED: 'accessRevoked',
}

/**
 * First match wins.
 *
 * `active` is the ConnectionCredentialsListItem from GET /connection-credentials,
 * `review` the {sessionId, reviewId, accessDurationSec} pointer, and `role` the
 * connection as the drawer listed it.
 *
 * Deliberately derived from durable state only — never from the flow status,
 * which every step of every flow overwrites. Reading the status here is what
 * let one failed poll drop a waiting row back to "Ask access", and clicking
 * that opens a SECOND review server-side, because the gateway does not dedupe.
 *
 * Note a JIT access rule is invisible here: ToOpenApi does not populate
 * jit_access_duration_sec on the list endpoint, only Get does. A JIT-gated role
 * therefore collapses as IDLE and reveals the review requirement on expand,
 * when the row fetches the connection — same as the CLJS flow.
 */
export function deriveRowState(role, active, review) {
  const hasSession = hasLiveSession(active)

  if (isPendingReviewFor(review, active)) {
    return ROW_STATE.PENDING_REVIEW
  }

  // An admin can disable connect while a credential is still live. The row has
  // to survive that, otherwise the only way to disconnect disappears.
  if (hasSession && role.accessModeConnect !== 'enabled') return ROW_STATE.ACCESS_REVOKED

  if (hasSession) {
    return active.expire_at ? ROW_STATE.ACTIVE_BOUNDED : ROW_STATE.ACTIVE_PERSISTENT
  }

  if (role.status !== 'online') return ROW_STATE.OFFLINE
  if (role.reviewers?.length) return ROW_STATE.NEEDS_REVIEW

  return ROW_STATE.IDLE
}
