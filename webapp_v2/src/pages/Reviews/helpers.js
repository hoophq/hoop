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

// Set on a review filed for one risk level of an analyzer rule.
const RISK_LEVEL = {
  high: { label: 'High risk', color: 'red' },
  medium: { label: 'Medium risk', color: 'yellow' },
  low: { label: 'Low risk', color: 'green' },
}
export const riskLevel = (review) => RISK_LEVEL[review?.risk_level] ?? null

// Filed by a hand-off agent that chose "Hold for approval" (ENG-661).
export const isHandoffReview = (review) => review?.via_handoff === true
export const HANDOFF_ROUTE = 'Hand-off → Hold for approval'

// How the review found its approvers when its level uses AI routing
// (ENG-663). Absent for a static level. `mode` "static" means the AI routing
// fell back and every candidate group got the review.
export const ROUTING_AI = 'ai'
export const reviewRouting = (review) => (review?.routing && typeof review.routing === 'object' ? review.routing : null)

const FALLBACK_CAUSE = {
  no_provider: 'No AI provider is set up.',
  timeout: 'The AI agent did not answer in time.',
  provider_error: 'The AI provider returned an error.',
  no_tool_call: 'The AI agent did not return exactly one decision.',
  refusal: 'The AI agent refused to decide.',
  invalid_decision: 'The AI agent returned a decision that is not valid.',
  no_valid_group: 'The AI agent selected no valid group.',
}

// The fallback cause in plain words. A cause this build does not know shows as sent.
export const fallbackCauseLabel = (cause) =>
  FALLBACK_CAUSE[cause] ?? (cause ? `AI routing did not complete (${cause}).` : 'AI routing did not complete.')

const INTEGRATION_LABEL = { slack: 'Slack', teams: 'MS Teams' }
export const integrationLabel = (key) => INTEGRATION_LABEL[key] ?? key

// One entry of routing.integrations: a notify key and its state when the AI
// decided. An integration that was not set up notified nobody.
const DELIVERY_LABEL = { dm: 'Direct message', channel: 'Channel', both: 'Direct message and channel' }
export function notifyIntegrationLabel(entry) {
  const name = integrationLabel(entry?.name)
  if (entry?.integrated !== true) return `${name}: not set up`
  return `${name}: ${DELIVERY_LABEL[entry.delivery] ?? DELIVERY_LABEL.dm}`
}

// routing.notify_fallback: the routed notifications reached nobody, so the
// gateway tried to post the review as one without routing (the listener's
// Slack channels or the default channel). routing.notify_fallback_posted
// says whether a channel accepted it. A value this build does not know shows
// as sent.
const NOTIFY_FALLBACK = {
  no_integration: 'No notify integration is set up.',
  no_target: 'The AI agent selected nobody it could reach.',
  not_delivered: 'Every routed notification failed.',
}
export const notifyFallbackLabel = (value, posted) =>
  `${NOTIFY_FALLBACK[value] ?? `The routed notifications reached nobody (${value}).`} ` +
  (posted === true ? 'Posted as without routing.' : 'The post as without routing reached nobody either.')

// The table's rule cell: the approval rule, else the risk level and the route.
export function ruleLabel(review) {
  if (review?.access_request_rule_name) return review.access_request_rule_name
  const parts = [riskLevel(review)?.label, isHandoffReview(review) ? 'Hand-off' : null]
  return parts.filter(Boolean).join(' · ') || null
}

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
