package events

import (
	"strings"
	"time"
)

type SessionEventBase struct {
	SessionID  string
	User       string
	Connection string
	OccurredAt time.Time
}

func newBase(sessionID, userEmail, connectionName string, occurredAt time.Time) SessionEventBase {
	return SessionEventBase{
		SessionID:  sessionID,
		User:       userEmail,
		Connection: connectionName,
		OccurredAt: occurredAt,
	}
}

func PublishSessionStarted(orgID string, base SessionEventBase, verb string) {
	Publish(orgID, "session.started", map[string]any{
		"session_id":  base.SessionID,
		"user":        base.User,
		"connection":  base.Connection,
		"verb":        verb,
		"occurred_at": base.OccurredAt.UTC().Format(time.RFC3339),
	}, "audit.session_open", base.SessionID+":session.started")
}

func PublishSessionClosed(orgID string, base SessionEventBase, exitCode string, durationMS int64) {
	Publish(orgID, "session.closed", map[string]any{
		"session_id":  base.SessionID,
		"user":        base.User,
		"connection":  base.Connection,
		"exit_code":   exitCode,
		"duration_ms": durationMS,
		"occurred_at": base.OccurredAt.UTC().Format(time.RFC3339),
	}, "audit.session_close", base.SessionID+":session.closed")
}

func PublishGuardrailViolation(orgID string, base SessionEventBase, ruleName string, matchedWords []string) {
	queryExcerpt := ""
	if len(matchedWords) > 0 {
		queryExcerpt = strings.Join(matchedWords, ", ")
		if len(queryExcerpt) > 256 {
			queryExcerpt = queryExcerpt[:256]
		}
	}
	Publish(orgID, "session.guardrail_violation", map[string]any{
		"session_id":    base.SessionID,
		"user":          base.User,
		"connection":    base.Connection,
		"rule":          ruleName,
		"query_excerpt": queryExcerpt,
		"occurred_at":   base.OccurredAt.UTC().Format(time.RFC3339),
	}, "audit.session_close.guardrails", base.SessionID+":session.guardrail_violation:"+ruleName)
}

func PublishJITApproved(orgID string, base SessionEventBase, reviewerEmail, reviewID string) {
	Publish(orgID, "access.jit_approved", map[string]any{
		"session_id":  base.SessionID,
		"user":        base.User,
		"connection":  base.Connection,
		"reviewer":    reviewerEmail,
		"review_id":   reviewID,
		"occurred_at": base.OccurredAt.UTC().Format(time.RFC3339),
	}, "review.status_change", reviewID+":access.jit_approved")
}

func PublishJITDenied(orgID string, base SessionEventBase, reviewerEmail, reviewID, reason string) {
	Publish(orgID, "access.jit_denied", map[string]any{
		"session_id":  base.SessionID,
		"user":        base.User,
		"connection":  base.Connection,
		"reviewer":    reviewerEmail,
		"review_id":   reviewID,
		"reason":      reason,
		"occurred_at": base.OccurredAt.UTC().Format(time.RFC3339),
	}, "review.status_change", reviewID+":access.jit_denied")
}

// PublishSensitiveDataDetected emits alert.sensitive_data_detected. The "types" are whatever the
// configured DLP provider reports (Presidio entity names, GCP DLP info types, etc.) — these are
// heuristic matches, not a guarantee that the value is regulated PII. Use PublishDataMasked when
// the question is "was anything actually redacted".
func PublishSensitiveDataDetected(orgID string, base SessionEventBase, typesDetected []string, totalDetections int) {
	Publish(orgID, "alert.sensitive_data_detected", map[string]any{
		"session_id":       base.SessionID,
		"user":             base.User,
		"connection":       base.Connection,
		"types_detected":   typesDetected,
		"total_detections": totalDetections,
		"occurred_at":      base.OccurredAt.UTC().Format(time.RFC3339),
	}, "audit.session_close.sensitive_data", base.SessionID+":alert.sensitive_data_detected")
}

func PublishDataMasked(orgID string, base SessionEventBase, typesMasked []string, totalRedactions int64) {
	Publish(orgID, "alert.data_masked", map[string]any{
		"session_id":       base.SessionID,
		"user":             base.User,
		"connection":       base.Connection,
		"types_masked":     typesMasked,
		"total_redactions": totalRedactions,
		"occurred_at":      base.OccurredAt.UTC().Format(time.RFC3339),
	}, "audit.session_close.masking", base.SessionID+":alert.data_masked")
}

func PublishAnomalyDetected(orgID string, base SessionEventBase, riskLevel, reason string) {
	Publish(orgID, "session.anomaly_detected", map[string]any{
		"session_id":  base.SessionID,
		"user":        base.User,
		"connection":  base.Connection,
		"risk_level":  riskLevel,
		"reason":      reason,
		"occurred_at": base.OccurredAt.UTC().Format(time.RFC3339),
	}, "audit.session_close.anomaly", base.SessionID+":session.anomaly_detected")
}

func PublishSessionPCIScopeEntered(orgID string, base SessionEventBase, tags []string) {
	Publish(orgID, "session.pci_scope_entered", map[string]any{
		"session_id":  base.SessionID,
		"user":        base.User,
		"connection":  base.Connection,
		"tags":        tags,
		"occurred_at": base.OccurredAt.UTC().Format(time.RFC3339),
	}, "audit.session_open.pci", base.SessionID+":session.pci_scope_entered")
}
