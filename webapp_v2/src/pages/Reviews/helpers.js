import { roleToGroups } from '@/utils/roles'

export const STATUS = {
  PENDING: 'PENDING',
  APPROVED: 'APPROVED',
  REJECTED: 'REJECTED',
  REVOKED: 'REVOKED',
  PROCESSING: 'PROCESSING',
  EXECUTED: 'EXECUTED',
  UNKNOWN: 'UNKNOWN',
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
}

export const statusLabel = (status) =>
  STATUS_LABEL[status] ?? { label: status ?? 'Unknown', color: 'gray' }

export const STATUS_FILTERS = [
  { value: 'all', label: 'All', match: () => true },
  { value: 'waiting', label: 'Waiting', match: (r) => r.status === STATUS.PENDING },
  { value: 'settled', label: 'Settled', match: (r) => r.status !== STATUS.PENDING },
]

export const isSidecarReview = (review) => Boolean(review?.listener_name)

// Only the groups that decided. A group still pending carries no reviewer and
// no date, and the review's own status already says nobody has acted.
export const decidedGroups = (review) =>
  (review?.review_groups_data ?? []).filter((g) => g.status !== STATUS.PENDING)

// An approval from outside the review's groups answers 200 and changes nothing,
// so the button is what has to refuse it. Groups come from the role because
// useUserStore carries no raw list, which covers a control plane's two.
export function canApprove(review, { role, adminRoleName, approverRoleName }) {
  const mine = roleToGroups(role, adminRoleName, approverRoleName)
  return (review?.review_groups_data ?? []).some((g) => mine.includes(g.group))
}

// The gateway appends a group row for an admin who rejects.
export const canReject = (review, user) => user.isAdmin || canApprove(review, user)

export const isSettled = (review) =>
  review?.status !== STATUS.PENDING && review?.status !== STATUS.APPROVED

export function reviewSource(review, sidecarsById) {
  const listener = review?.listener_name
  if (!listener) return { primary: review?.connection_name || '—', secondary: null }
  return { primary: listener, secondary: sidecarsById.get(review.sidecar_id)?.name ?? null }
}

export function sortByNewest(reviews) {
  return [...reviews].sort(
    (a, b) => new Date(b.created_at ?? 0) - new Date(a.created_at ?? 0),
  )
}
