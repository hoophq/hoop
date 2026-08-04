import { FLOW_STATUS } from '@/stores/useNativeAccessStore'
import { isSessionValid } from './helpers'

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
 * `active` is the ConnectionCredentialsListItem from GET /connection-credentials;
 * `role` is the connection as the drawer listed it.
 *
 * Note a JIT access rule is invisible here: ToOpenApi does not populate
 * jit_access_duration_sec on the list endpoint, only Get does. A JIT-gated role
 * therefore collapses as IDLE and reveals the review requirement on expand,
 * when the row fetches the connection — same as the CLJS flow.
 */
export function deriveRowState(role, active, flowStatus) {
  if (flowStatus === FLOW_STATUS.PENDING_REVIEW) return ROW_STATE.PENDING_REVIEW

  const hasSession = isSessionValid(active) && Boolean(active?.session_id)

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
