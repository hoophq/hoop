import { roleToGroups } from '@/utils/roles'

export const STATUS = {
  PENDING: 'PENDING',
  APPROVED: 'APPROVED',
  REJECTED: 'REJECTED',
  REVOKED: 'REVOKED',
  PROCESSING: 'PROCESSING',
  EXECUTED: 'EXECUTED',
  UNKNOWN: 'UNKNOWN',
  EXPIRED: 'EXPIRED',
}

// APPROVED is released and waiting for the sidecar to retry; EXECUTED already ran.
const STATUS_LABEL = {
  [STATUS.PENDING]: { label: 'Pending', color: 'yellow' },
  [STATUS.APPROVED]: { label: 'Approved', color: 'blue' },
  [STATUS.EXECUTED]: { label: 'Released', color: 'green' },
  [STATUS.REJECTED]: { label: 'Rejected', color: 'red' },
  [STATUS.REVOKED]: { label: 'Revoked', color: 'red' },
  [STATUS.PROCESSING]: { label: 'Processing', color: 'gray' },
  [STATUS.UNKNOWN]: { label: 'Unknown', color: 'gray' },
  [STATUS.EXPIRED]: { label: 'Expired', color: 'gray' },
}

// A sidecar's EXECUTED is a statement it released; any other, a session that ran.
const EXECUTED_SESSION = { label: 'Executed', color: 'green' }

export function statusLabel(review) {
  const status = review?.status
  if (status === STATUS.EXECUTED && !isSidecarReview(review)) return EXECUTED_SESSION
  return STATUS_LABEL[status] ?? { label: status ?? 'Unknown', color: 'gray' }
}

// The sidebar picks one: Pending Approvals (/approvals) or Approval History
// (/approvals?status=settled).
export const STATUS_FILTERS = {
  waiting: (r) => r.status === STATUS.PENDING,
  settled: (r) => r.status !== STATUS.PENDING,
}

export const filterFromSearch = (searchParams) =>
  searchParams.get('status') === 'settled' ? 'settled' : 'waiting'

export const isSidecarReview = (review) => Boolean(review?.listener_name)

// Only the groups that decided. A group still pending carries no reviewer and
// no date, and the review's own status already says nobody has acted.
export const decidedGroups = (review) =>
  (review?.review_groups_data ?? []).filter((g) => g.status !== STATUS.PENDING)

// An approval from outside the review's groups answers 200 and changes nothing,
// so the button is what has to refuse it. `groups` is what /userinfo reports,
// the same list the backend intersects; the role is the fallback for a gateway
// that answers none.
export function canApprove(review, { groups, role, adminRoleName, approverRoleName }) {
  const mine = groups?.length ? groups : roleToGroups(role, adminRoleName, approverRoleName)
  return (review?.review_groups_data ?? []).some((g) => mine.includes(g.group))
}

// The gateway appends a group row for an admin who rejects.
export const canReject = (review, user) => user.isAdmin || canApprove(review, user)

// A sidecar review is onetime, and its approval can be revoked until the sidecar
// uses it. A gateway review keeps Reject: the gateway revokes only jit reviews.
export const canRevoke = (review) =>
  isSidecarReview(review) && review?.status === STATUS.APPROVED

export const isSettled = (review) =>
  review?.status !== STATUS.PENDING && review?.status !== STATUS.APPROVED

// The API sends expires_at only for a sidecar review with a time limit, and
// reports EXPIRED itself, so no client clock decides the status.
const EXPIRY_LABEL = {
  [STATUS.PENDING]: 'Decide by',
  [STATUS.APPROVED]: 'Approval expires at',
  [STATUS.EXPIRED]: 'Expired at',
}

export const expiryLabel = (review) =>
  (review?.expires_at && EXPIRY_LABEL[review.status]) || null

// What the review was filed against. `resource` is the mirror connection when
// the org has one, else the listener, else the connection of a connection
// review. `sidecar` and `listener` are set on a sidecar review; `listener` is
// only reported separately when the resource is not already the listener.
// `detail` is the table's subline.
export function reviewSource(review, sidecarsById) {
  const connection = review?.connection?.name
  const listenerName = review?.listener_name
  if (!listenerName) return { resource: connection ?? '—', sidecar: null, listener: null, detail: null }
  const sidecar = sidecarsById.get(review.sidecar_id)?.name ?? null
  const listener = connection ? listenerName : null
  return {
    resource: connection ?? listenerName,
    sidecar,
    listener,
    detail: [sidecar, listener].filter(Boolean).join(' · ') || null,
  }
}

export function sortByNewest(reviews) {
  return [...reviews].sort(
    (a, b) => new Date(b.created_at ?? 0) - new Date(a.created_at ?? 0),
  )
}
