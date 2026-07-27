// Whether a connection already has any review configuration set.
// Drives the backward-compatible Review section's visibility in both
// Terminal Access (Review by Command) and Native Access (Just-in-Time
// Review) tabs — same predicate the CLJS form uses.
export function hasReviewConfig(connection) {
  if (!connection) return false
  if ((connection.reviewers || []).length > 0) return true
  if (connection.min_review_approvals != null) return true
  if ((connection.force_approve_groups || []).length > 0) return true
  return false
}
