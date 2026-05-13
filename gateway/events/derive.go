package events

import (
	"strconv"
	"strings"
	"time"

	"github.com/hoophq/hoop/gateway/models"
)

// DeriveFromSessionStart is called from the audit plugin after SessionOpen
// persists the session row.
func DeriveFromSessionStart(orgID string, session *models.Session, conn *models.Connection) {
	if session == nil {
		return
	}
	base := newBase(session.ID, session.UserEmail, session.Connection, session.CreatedAt)
	PublishSessionStarted(orgID, base, session.Verb)

	if hasPCIScopeTag(conn) {
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

// DeriveFromSessionEnd is called from the audit plugin after SessionClose
// finalizes the session.
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

	PublishSessionClosed(orgID, base, exitCode, durationMS)

	for _, rule := range session.GuardRailsInfo {
		PublishGuardrailViolation(orgID, base, rule.RuleName, rule.MatchedWords)
	}

	if session.AIAnalysis != nil && strings.EqualFold(session.AIAnalysis.RiskLevel, "high") {
		PublishAnomalyDetected(orgID, base, session.AIAnalysis.RiskLevel, session.AIAnalysis.Explanation)
	}
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
