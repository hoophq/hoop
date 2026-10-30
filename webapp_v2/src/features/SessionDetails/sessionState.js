import { ROLE_ADMIN, ROLE_AUDITOR } from '@/utils/roles'

// Decisions about a session that more than one section reads.

export const hasReview = (session) => Boolean(session?.review?.id)

// A sidecar review session (verb exec, status open) holds a statement on a
// listener, not a script on a resource: there is nothing to kill or run again.
export const isSidecarReview = (session) => Boolean(session?.review?.listener_name)

// A reviewed exec still running in the agent: the local Execute click onwards,
// or the server signal when nothing local tracks it (the review reads
// PROCESSING while it runs and UNKNOWN after the gateway stopped waiting).
export function isRunningAsync(session, execution) {
  if (session.verb !== 'exec' || !hasReview(session) || session.status === 'done') return false
  if (execution) return ['executing', 'running', 'done'].includes(execution.status)
  return ['PROCESSING', 'UNKNOWN'].includes(session.review.status)
}

// The approver groups of the user still waiting to decide a pending review.
export function canReview(session, user) {
  const groups = new Set(user?.groups || [])
  const pending = (session.review?.review_groups_data || []).some(
    (group) => group.status === 'PENDING' && groups.has(group.group),
  )
  return pending && session.review?.status === 'PENDING'
}

// The output waits for a decision, an execution or a connection. A JIT
// session holds credentials, not output.
export function isOutputHidden(session, execution) {
  const waiting = (session.review?.review_groups_data || []).some((group) => group.status === 'PENDING')
  return (
    Boolean(session.metadata?.credentials_expire_at) ||
    session.status === 'ready' ||
    isRunningAsync(session, execution) ||
    (waiting && session.review?.status === 'PENDING')
  )
}

// Keys the Metadata tab leaves out: the action bar reads the credentials keys,
// and the sidecar keys render as links.
export const HIDDEN_METADATA_KEYS = new Set([
  'credentials_expire_at',
  'credentials_revoked_at',
  'credential_session',
  'sidecar',
  'sidecar_session_id',
])

// The gateway serves a session file to its owner, an admin or an auditor
// (session.go); NB01's input Download shows only to them, by the /userinfo
// role. The large-input card keeps the ClojureScript rule (InputTab).
export function canDownloadInput(session, { user, role } = {}) {
  if (role === ROLE_ADMIN || role === ROLE_AUDITOR) return true
  return Boolean(session?.user_id) && session.user_id === user?.id
}

// The badge of a review or of one of its groups. A review that runs
// (PROCESSING) or ran (EXECUTED) was approved first (matrix E1). REVOKED,
// UNKNOWN and EXPIRED draw no badge.
export function reviewBadgeStatus(status) {
  if (status === 'PROCESSING' || status === 'EXECUTED') return 'APPROVED'
  return ['APPROVED', 'PENDING', 'REJECTED'].includes(status) ? status : null
}

// Who ran an approved review, when it was not the user who created the session
// (NB12). A review the MCP federation reset to APPROVED did not finish its run.
export function executedBy({ review, user } = {}) {
  const executor = review?.executed_by
  if (!executor || executor === user) return null
  return review.status === 'PROCESSING' || review.status === 'EXECUTED' ? executor : null
}

// The insight cards by tab (NB19): the AI Session Analyzer and the input
// guardrails judged the input, the output guardrails the output. A guardrail
// entry with no direction is an input rule, as the card labels it.
export function insightsByTab({ guardrails_info: guardrails = [], ai_analysis: analysis } = {}) {
  const entries = guardrails || []
  return {
    input: {
      analysis: analysis && Object.keys(analysis).length ? analysis : null,
      guardrails: entries.filter((entry) => entry.direction !== 'output'),
    },
    output: { guardrails: entries.filter((entry) => entry.direction === 'output') },
  }
}

// The tab that holds why a session was blocked, so it opens first; null when
// nothing blocked it. The input side wins: it stopped the run first.
export function blockReasonTab(session) {
  const { input, output } = insightsByTab(session)
  if (input.analysis?.action === 'block_execution' || input.guardrails.length) return 'input'
  if (output.guardrails.length) return 'output'
  return null
}

// A finished session that a guardrail or the AI Session Analyzer stopped was
// blocked, one whose review was rejected never ran, and an exec that exited
// with an error failed: none of them succeeded. A connect session's exit code
// is the client's last one, so it does not count.
export function sessionStatusKey({ status, verb, guardrails_info: guardrails, ai_analysis: analysis, review, exit_code: exitCode }) {
  if (status !== 'done') return status
  if (guardrails?.length || analysis?.action === 'block_execution') return 'blocked'
  if (review?.status === 'REJECTED') return 'rejected'
  if (verb === 'exec' && exitCode != null && exitCode !== 0) return 'failed'
  return status
}
