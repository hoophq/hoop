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

// AIDecision is what the analyzer verdict resolves to for a synchronous session
// execution (an exec or a runbook run).
//
// This is the whole-session counterpart to aianalyzer.Outcome (allow/warn/block),
// which decides per-request handling for native HTTP proxy traffic. An exec/runbook
// session is asynchronous and reviewable, so require_access_request maps to a real
// review here rather than degrading to a warning.
type AIDecision int

const (
	// AIDecisionProceed means the caller should continue: persist the session
	// (services.CreateSession persists session.AIAnalysis) and run it, then attach
	// the analysis to its final/timeout response via ToOpenApiSessionAIAnalysis.
	AIDecisionProceed AIDecision = iota
	// AIDecisionBlocked means the run was blocked by the analyzer. The caller must
	// write the returned response and stop.
	AIDecisionBlocked
	// AIDecisionReview means the run requires an access request. The caller must
	// write the returned response with status 202 and stop.
	AIDecisionReview
)

// ApplyAIAnalysisDecision resolves an AI analyzer verdict into a session-execution
// decision and applies its side effects. It is shared by the Web Terminal/API exec
// handler (Post) and the runbook exec handler so the block / require-access-request /
// allow behavior stays identical across both surfaces.
//
//   - analysis == nil                  -> (AIDecisionProceed, nil, nil) (no rule / empty script)
//   - action == allow_execution        -> sets session.AIAnalysis; (AIDecisionProceed, nil, nil)
//   - action == block_execution        -> marks the session done, persists it, emits the
//     full session lifecycle (start+end) events, tracks the finished session, and returns
//     a blocked response; (AIDecisionBlocked, response, nil)
//   - action == require_access_request -> persists the session, creates a one-time review,
//     emits the session start event, and returns a 202 review response; (AIDecisionReview, response, nil)
//
// On AIDecisionProceed the caller owns persisting and running the session. The session
// pointer is mutated in place (AIAnalysis is always attached; status/exit/end are set on
// block) so the caller's subsequent persistence carries the analysis.
//
// inputEnvVars and inputClientArgs are only consumed by the review path: they are stored
// on the one-time review so the session can be re-executed verbatim once approved. The
// script reviewed is session.BlobInput, which both callers already populate.
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

		// AI-blocked sessions never reach the normal create/run path, so we publish the
		// full session lifecycle (started + closed) here to keep downstream event
		// consumers consistent.
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
		// Persist the session row (status stays open) so the pending review references a
		// real session and its ai_analysis is recorded. The session executes after the
		// review is approved, at which point the agent and downstream consumers (e.g.
		// analyzer metrics) expect the row to exist.
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
