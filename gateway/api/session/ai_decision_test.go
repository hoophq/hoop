package sessionapi

import (
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// TestApplyAIAnalysisDecision_NonEnforcingPaths covers the branches that do not touch the
// database, events, or analytics: a nil analysis (no rule / empty script) and an
// allow_execution verdict. The block_execution and require_access_request branches write
// sessions, emit lifecycle events, and create reviews, which require a live datastore and
// are exercised by integration tests rather than here.
func TestApplyAIAnalysisDecision_NonEnforcingPaths(t *testing.T) {
	ctx := storagev2.NewContext("user-id", "org-id")
	conn := &models.Connection{Name: "pgdemo"}

	t.Run("nil analysis proceeds without mutating the session", func(t *testing.T) {
		session := &models.Session{ID: "sid-1"}
		decision, resp, err := ApplyAIAnalysisDecision(ctx, session, conn, nil, nil, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if decision != AIDecisionProceed {
			t.Errorf("decision = %v, want AIDecisionProceed", decision)
		}
		if resp != nil {
			t.Errorf("resp = %+v, want nil", resp)
		}
		if session.AIAnalysis != nil {
			t.Errorf("session.AIAnalysis = %+v, want nil", session.AIAnalysis)
		}
	})

	t.Run("allow_execution proceeds and attaches the analysis", func(t *testing.T) {
		session := &models.Session{ID: "sid-2"}
		analysis := &models.SessionAIAnalysis{
			RiskLevel:   "low",
			Title:       "Safe read",
			Explanation: "read-only query",
			Action:      string(models.AllowExecution),
		}
		decision, resp, err := ApplyAIAnalysisDecision(ctx, session, conn, analysis, nil, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if decision != AIDecisionProceed {
			t.Errorf("decision = %v, want AIDecisionProceed", decision)
		}
		if resp != nil {
			t.Errorf("resp = %+v, want nil", resp)
		}
		if session.AIAnalysis != analysis {
			t.Errorf("session.AIAnalysis = %+v, want the passed analysis", session.AIAnalysis)
		}
	})
}
