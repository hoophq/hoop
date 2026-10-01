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

// The review carries no connection name, so a review filed against one has
// nothing to show here. A control plane stores none; the gateway is where they
// exist, and this page is not its queue.
export function reviewSource(review, sidecarsById) {
  const listener = review?.listener_name
  if (!listener) return { primary: '—', secondary: null }
  return { primary: listener, secondary: sidecarsById.get(review.sidecar_id)?.name ?? null }
}

export function sortByNewest(reviews) {
  return [...reviews].sort(
    (a, b) => new Date(b.created_at ?? 0) - new Date(a.created_at ?? 0),
  )
}

// The session labels a control plane writes for the caller who filed a sidecar review.
export const REQUESTER_LABEL = {
  subject: 'sidecar.requester.subject',
  email: 'sidecar.requester.email',
  peerAddr: 'sidecar.requester.peer_addr',
  method: 'sidecar.requester.method',
}

// How the sidecar established the filer. Keep the descriptions in sync with
// requesterMethodLabel in gateway/api/sidecar/requester.go.
export const REQUESTER_METHOD = {
  google_identity: {
    label: 'Google token',
    color: 'blue',
    description: 'Google token holder, checked with Google by the sidecar (audience not checked)',
  },
  ssh_certificate: {
    label: 'SSH certificate',
    color: 'blue',
    description: "SSH certificate signed by the listener's trusted CA, checked by the sidecar",
  },
  database_user: {
    label: 'Database login claim',
    color: 'yellow',
    description: 'Login name the client claimed before authenticating, not checked',
  },
  identity_header: {
    label: 'Proxy header',
    color: 'yellow',
    description:
      'Header value; any client that can reach the listener can set it unless a proxy strips it',
  },
  tls_client_certificate: {
    label: 'TLS name',
    color: 'yellow',
    description: 'TLS certificate name, not verified',
  },
  unspecified: { label: 'Not reported', color: 'yellow', description: 'Identity source not reported' },
  peer_address: { label: 'Address only', color: 'gray', description: 'Network address only, no identity' },
}

// The filer from GET /sessions/:id labels, or null when the review names none.
export function requesterFromLabels(labels) {
  if (!labels || typeof labels !== 'object') return null
  const requester = {
    subject: labels[REQUESTER_LABEL.subject] ?? '',
    email: labels[REQUESTER_LABEL.email] ?? '',
    peerAddr: labels[REQUESTER_LABEL.peerAddr] ?? '',
    method: labels[REQUESTER_LABEL.method] ?? '',
  }
  return Object.values(requester).some(Boolean) ? requester : null
}

export const requesterName = (requester) =>
  requester?.subject || requester?.email || requester?.peerAddr || 'Unknown caller'

// A missing method reads as not reported; an unknown one is shown as sent.
// hasOwn, so a method named like an Object.prototype key is unknown too.
export function requesterMethod(method) {
  const key = method || 'unspecified'
  if (Object.hasOwn(REQUESTER_METHOD, key)) return REQUESTER_METHOD[key]
  return { label: `Reported as ${key}`, color: 'yellow', description: `Source reported as ${key}` }
}
