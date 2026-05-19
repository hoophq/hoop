package events

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
)

// DeriveFromSessionStart is called from the audit plugin after SessionOpen persists the session row.
// we will dervice session start event for the conenction
func DeriveFromSessionStart(orgID string, session *models.Session, conn *models.Connection) {
	if session == nil {
		return
	}
	base := newBase(session.ID, session.UserEmail, session.Connection, session.CreatedAt)
	PublishSessionStarted(orgID, base, session.Verb)

	if hasPCIScopeTag(conn) {
		// this event is basse on the connection/role tags
		// if the connection has pci-scope tag, we will publish this event.
		PublishSessionPCIScopeEntered(orgID, base, extractConnectionTags(conn))
	}
}

// DeriveFromReview is called when a review transitions to APPROVED or REJECTED.
func DeriveFromReview(orgID string, review *models.Review, session *models.Session) {
	if review == nil || session == nil {
		return
	}
	base := newBase(session.ID, session.UserEmail, session.Connection, time.Now().UTC())

	reviewerEmail := resolveReviewerEmail(review)

	switch review.Status {
	case models.ReviewStatusApproved:
		PublishJITApproved(orgID, base, reviewerEmail, review.ID)
	case models.ReviewStatusRejected:
		reason := ""
		if review.RejectionReason != nil {
			reason = *review.RejectionReason
		}
		PublishJITDenied(orgID, base, reviewerEmail, review.ID, reason)
	}
}

// DeriveFromSessionEnd is called from the audit plugin after SessionClose finalizes the session.
func DeriveFromSessionEnd(orgID string, session *models.Session) {
	if session == nil {
		return
	}
	base := newBase(session.ID, session.UserEmail, session.Connection, time.Now().UTC())

	exitCode := ""
	if session.ExitCode != nil {
		exitCode = strconv.Itoa(*session.ExitCode)
	}

	var durationMS int64
	if session.EndSession != nil {
		durationMS = session.EndSession.Sub(session.CreatedAt).Milliseconds()
	}

	// when the session close we publish the sessio close event.
	PublishSessionClosed(orgID, base, exitCode, durationMS)

	for _, rule := range session.GuardRailsInfo {
		// if the guardrails is violated we will publish the guardrail violation event.
		PublishGuardrailViolation(orgID, base, rule.RuleName, rule.MatchedWords)
	}

	if session.AIAnalysis != nil && strings.EqualFold(session.AIAnalysis.RiskLevel, "high") {
		// this event the AI analyzer will detect
		PublishAnomalyDetected(orgID, base, session.AIAnalysis.RiskLevel, session.AIAnalysis.Explanation)
	}

	// alert.sensitive_data_detected: fires whenever the DLP analyzer reported any entity for
	// this session, regardless of whether it was masked. The entity catalog is provider-defined
	// and includes both strict PII (EMAIL_ADDRESS, US_SSN, …) and looser categories that may not
	// be PII at all (LOCATION, DATE_TIME, …) — the event payload exposes the raw type names so
	// consumers can apply their own classification. Counts come from the per-info-type aggregate
	// the analyzer-metrics packet handler accumulates in sessions.metrics.data_analyzer.
	if types, total := analyzerTotalsFromSession(session); total > 0 {
		PublishSensitiveDataDetected(orgID, base, types, total)
	}

	// alert.data_masked: fires only when the redactor actually replaced bytes for this
	// session. Counts come from private.session_metrics where IncrementSessionMaskedMetrics
	// records each SUCCESS redaction summary written through the audit/WAL pipeline.
	if maskedTotals, err := models.GetSessionMaskedTotals(models.DB, session.ID); err != nil {
		log.Warnf("event-routing: failed loading masked totals for session %s: %v", session.ID, err)
	} else if types, total := mapToSortedTypesAndTotal(maskedTotals); total > 0 {
		PublishDataMasked(orgID, base, types, total)
	}
}

// analyzerTotalsFromSession reads sessions.metrics["data_analyzer"] and returns the
// sorted list of info types plus the summed count. JSON numbers decode as float64 by
// default in map[string]any; json.Number is also supported for completeness so future
// callers that switch the decoder don't silently produce zero totals.
func analyzerTotalsFromSession(session *models.Session) ([]string, int) {
	if session == nil || session.Metrics == nil {
		return nil, 0
	}
	raw, ok := session.Metrics["data_analyzer"]
	if !ok {
		return nil, 0
	}
	inner, ok := raw.(map[string]any)
	if !ok {
		return nil, 0
	}
	totals := make(map[string]int64, len(inner))
	for infoType, v := range inner {
		switch n := v.(type) {
		case float64:
			totals[infoType] = int64(n)
		case int:
			totals[infoType] = int64(n)
		case int64:
			totals[infoType] = n
		case json.Number:
			if i, err := n.Int64(); err == nil {
				totals[infoType] = i
			}
		}
	}
	types, total := mapToSortedTypesAndTotal(totals)
	// total_detections is exposed as int; the analyzer counter is bounded by the size of a
	// single session's output so int is safe even on 32-bit builds.
	return types, int(total)
}

// mapToSortedTypesAndTotal returns the keys of totals (sorted, excluding zero values)
// and the sum of the values. A stable ordering keeps event payloads deterministic for
// downstream consumers.
func mapToSortedTypesAndTotal(totals map[string]int64) ([]string, int64) {
	types := make([]string, 0, len(totals))
	var sum int64
	for infoType, count := range totals {
		if count <= 0 {
			continue
		}
		types = append(types, infoType)
		sum += count
	}
	sort.Strings(types)
	return types, sum
}

func resolveReviewerEmail(review *models.Review) string {
	for _, rg := range review.ReviewGroups {
		if rg.Status == models.ReviewStatusApproved || rg.Status == models.ReviewStatusRejected {
			if rg.OwnerEmail != nil {
				return *rg.OwnerEmail
			}
		}
	}
	return ""
}

func hasPCIScopeTag(conn *models.Connection) bool {
	if conn == nil || conn.Tags == nil {
		return false
	}
	for _, tag := range conn.Tags {
		if strings.EqualFold(tag, "pci") || strings.EqualFold(tag, "pci-scope") {
			return true
		}
	}
	return false
}

func extractConnectionTags(conn *models.Connection) []string {
	if conn == nil {
		return nil
	}
	return []string(conn.Tags)
}
