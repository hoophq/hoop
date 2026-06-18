package sessionapi

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/clientexec"
	"github.com/hoophq/hoop/gateway/events"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// AIDecision is the analyzer verdict resolved for a synchronous session run (exec
// or runbook). Unlike aianalyzer.Outcome (per-request HTTP proxy handling), these
// runs are async and reviewable, so require_access_request maps to a real review
// instead of a warning.
type AIDecision int

const (
	// AIDecisionProceed: caller persists and runs the session, with analysis attached.
	AIDecisionProceed AIDecision = iota
	// AIDecisionBlocked: blocked by the analyzer; caller writes the response and stops.
	AIDecisionBlocked
	// AIDecisionReview: requires an access request; caller writes the 202 response and stops.
	AIDecisionReview
)

// ApplyAIAnalysisDecision resolves an analyzer verdict into a session decision and
// applies its side effects. Shared by the exec handler (Post) and the runbook handler.
//
//   - analysis == nil / allow_execution -> AIDecisionProceed (analysis attached on allow)
//   - block_execution        -> marks the session done, persists it, emits start+end
//     events, tracks usage, returns a blocked response (AIDecisionBlocked)
//   - require_access_request -> persists the session, creates a one-time review, emits
//     the start event, returns a 202 review response (AIDecisionReview)
//
// On Proceed the caller owns persistence and execution; the session is mutated in place.
// inputEnvVars/inputClientArgs are used only by the review path, to re-run the session
// verbatim once approved (the reviewed script is session.BlobInput).
func ApplyAIAnalysisDecision(
	ctx *storagev2.Context,
	session *models.Session,
	conn *models.Connection,
	analysis *models.SessionAIAnalysis,
	accessRule *models.AccessRequestRule,
	inputEnvVars map[string]string,
	inputClientArgs []string,
) (AIDecision, *clientexec.Response, error) {
	if analysis == nil {
		return AIDecisionProceed, nil, nil
	}

	session.AIAnalysis = analysis

	switch analysis.Action {
	case string(models.BlockExecution):
		session.Status = string(openapi.SessionStatusDone)
		session.ExitCode = &InternalExitCode
		endTime := time.Now().UTC()
		session.EndSession = &endTime

		if err := models.UpsertSession(*session); err != nil {
			return AIDecisionProceed, nil, fmt.Errorf("failed updating blocked session: %w", err)
		}

		// Blocked sessions skip the normal create/run path, so emit the full
		// lifecycle (start+end) here to keep event consumers consistent.
		events.DeriveFromSessionStart(ctx.OrgID, session, conn)
		events.DeriveFromSessionEnd(ctx.OrgID, session)

		trackClient := analytics.New()
		defer trackClient.Close()
		trackClient.TrackSessionUsageData(analytics.EventSessionFinished, ctx.OrgID, ctx.UserID, session.ID)

		return AIDecisionBlocked, &clientexec.Response{
			SessionID:         session.ID,
			Output:            "Session blocked by AI risk analyzer",
			OutputStatus:      "blocked",
			ExitCode:          InternalExitCode,
			ExecutionTimeMili: 0,
			AIAnalysis:        ToOpenApiSessionAIAnalysis(analysis),
		}, nil

	case string(models.RequireAccessRequest):
		if accessRule == nil {
			return AIDecisionProceed, nil, fmt.Errorf("ai analyzer requested review without resolving access request rule")
		}
		orgID, err := uuid.Parse(ctx.GetOrgID())
		if err != nil {
			return AIDecisionProceed, nil, fmt.Errorf("invalid org id: %w", err)
		}
		// Persist the session (status stays open) so the review references a real row;
		// it executes after approval, when downstream consumers expect the row to exist.
		if err := models.UpsertSession(*session); err != nil {
			return AIDecisionProceed, nil, fmt.Errorf("failed persisting session for ai review: %w", err)
		}
		review, err := CreateReviewFromAIAnalysis(orgID, session.ID, conn,
			AIReviewRequester{
				UserID:      ctx.UserID,
				UserEmail:   ctx.UserEmail,
				UserName:    ctx.UserName,
				UserSlackID: ctx.SlackID,
				UserGroups:  ctx.UserGroups,
			},
			accessRule, string(session.BlobInput), inputEnvVars, inputClientArgs)
		if err != nil {
			return AIDecisionProceed, nil, fmt.Errorf("failed creating ai-driven review: %w", err)
		}

		events.DeriveFromSessionStart(ctx.OrgID, session, conn)

		return AIDecisionReview, &clientexec.Response{
			HasReview:  true,
			Output:     fmt.Sprintf("%s/reviews/%s", appconfig.Get().FullApiURL(), review.ID),
			SessionID:  session.ID,
			AIAnalysis: ToOpenApiSessionAIAnalysis(analysis),
		}, nil

	default:
		// allow_execution (or any non-enforcing action): proceed; analysis is attached.
		return AIDecisionProceed, nil, nil
	}
}
