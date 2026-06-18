package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/api/openapi"
	sessionapi "github.com/hoophq/hoop/gateway/api/session"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/clientexec"
	"github.com/hoophq/hoop/gateway/events"
	"github.com/hoophq/hoop/gateway/jira"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const execTimeout = 50 * time.Second

type execInput struct {
	ConnectionName string            `json:"connection_name" jsonschema:"name of the Hoop connection to execute against"`
	Input          string            `json:"input" jsonschema:"the query or command to run (e.g. a SQL statement for a database connection)"`
	Args           []string          `json:"args,omitempty" jsonschema:"optional client arguments forwarded to the connection (e.g. CLI flags)"`
	EnvVars        map[string]string `json:"env_vars,omitempty" jsonschema:"optional environment variables forwarded to the connection"`
	Metadata       map[string]any    `json:"metadata,omitempty" jsonschema:"optional metadata fields to attach to the session (e.g. ticket number, CI job URL)"`
	JiraFields     map[string]string `json:"jira_fields,omitempty" jsonschema:"optional Jira issue fields (e.g. summary, description) - only used if the connection has a Jira issue template configured"`
}

func registerExecTools(server *mcp.Server) {
	openWorld := false

	mcp.AddTool(server, &mcp.Tool{
		Name: "exec",
		Description: "Run a one-shot command or query against a Hoop connection on behalf of the authenticated user. " +
			"Mirrors `hoop exec`. Returns one of three envelopes: `status=completed` with output, " +
			"`status=pending_approval` with a review_id (call reviews_wait to long-poll; once APPROVED call reviews_execute), " +
			"or `status=running` with a session_id (after a 50s timeout; poll sessions_get). " +
			"Authorization, data masking, guardrails, and review gates are enforced by the gateway — " +
			"this tool does not bypass any of them.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: &openWorld},
	}, execHandler)
}

func execHandler(ctx context.Context, _ *mcp.CallToolRequest, args execInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}
	token := accessTokenFrom(ctx)
	if token == "" {
		return errResult("unauthorized: missing access token"), nil, nil
	}
	if args.ConnectionName == "" {
		return errResult("connection_name is required"), nil, nil
	}
	if args.Input == "" {
		return errResult("input is required"), nil, nil
	}

	// Confirm the connection exists / the user can see it. The downstream
	// gateway auth chain will re-check access; this is a fast-fail for a
	// clearer error message.
	conn, err := models.GetConnectionByNameOrID(sc, args.ConnectionName)
	if err != nil {
		return nil, nil, fmt.Errorf("failed looking up connection: %w", err)
	}
	if conn == nil {
		return errResult(fmt.Sprintf("connection %q not found or not accessible", args.ConnectionName)), nil, nil
	}

	trackClient := analytics.New()
	defer trackClient.Close()

	// Origin=client (matches `hoop exec` CLI) triggers full audit persistence:
	// the audit plugin inserts the session row on OnConnect, stores the query
	// via UpdateSessionInput, and runs AI analysis on OnReceive. Origin=client-api
	// is reserved for HTTP flows that pre-insert the session row themselves.
	sessionID := uuid.NewString()
	newSession := models.Session{
		ID:                   sessionID,
		OrgID:                sc.GetOrgID(),
		Labels:               nil,
		Metadata:             args.Metadata,
		IntegrationsMetadata: nil,
		Metrics:              nil,
		BlobInput:            models.BlobInputType(args.Input),
		UserEmail:            sc.UserEmail,
		UserID:               sc.UserID,
		UserName:             sc.UserName,
		ConnectionType:       conn.Type,
		ConnectionSubtype:    conn.SubType.String,
		Connection:           conn.Name,
		ConnectionTags:       conn.ConnectionTags,
		Verb:                 pb.ClientVerbExec,
		Status:               string(openapi.SessionStatusOpen),
		IdentityType:         "user",
		SessionBatchID:       nil,
		CorrelationID:        nil,
		Origin:               pb.SessionOriginMCP,
		CreatedAt:            time.Now().UTC(),
		EndSession:           nil,
	}

	orgID := uuid.MustParse(sc.GetOrgID())
	needsAiReview := false
	analyzeRes, aiAccessRule, err := sessionapi.AIAnalyze(ctx, orgID, conn.Name, args.Input)
	if err != nil {
		return nil, nil, fmt.Errorf("failed analyzing session: %v", err)
	}
	if analyzeRes != nil {
		newSession.AIAnalysis = analyzeRes

		shouldBlock := analyzeRes.Action == string(models.BlockExecution)
		needsAiReview = analyzeRes.Action == string(models.RequireAccessRequest)
		if shouldBlock {
			newSession.Status = string(openapi.SessionStatusDone)
			newSession.ExitCode = &sessionapi.InternalExitCode
			endTime := time.Now().UTC()
			newSession.EndSession = &endTime
		}

		if err := models.UpsertSession(newSession); err != nil {
			return nil, nil, fmt.Errorf("failed upserting session with AI analysis results: %v", err)
		}

		if shouldBlock {
			// AI-blocked sessions never reach the create path below, so we publish the full
			// session lifecycle (started + closed) here to keep downstream event consumers
			// consistent.
			events.DeriveFromSessionStart(sc.OrgID, &newSession, conn)
			events.DeriveFromSessionEnd(sc.OrgID, &newSession)

			trackClient.TrackSessionUsageData(analytics.EventSessionFinished, sc.OrgID, sc.UserID, sessionID)

			return execResponseToEnvelope(&clientexec.Response{
				SessionID:         sessionID,
				Output:            "Session blocked by AI risk analyzer",
				OutputStatus:      "blocked",
				ExitCode:          sessionapi.InternalExitCode,
				ExecutionTimeMili: 0,
				AIAnalysis:        sessionapi.ToOpenApiSessionAIAnalysis(analyzeRes),
			}, sessionID)
		}
	}

	if conn.JiraIssueTemplateID.String != "" {
		issueTemplate, jiraConfig, err := models.GetJiraIssueTemplatesByID(conn.OrgID, conn.JiraIssueTemplateID.String)
		if err != nil {
			return nil, nil, fmt.Errorf("failed obtaining jira issue template for %v: %v", conn.Name, err)
		}
		if jiraConfig != nil && jiraConfig.IsActive() {
			if args.JiraFields == nil {
				args.JiraFields = map[string]string{}
			}
			jiraFields, err := jira.ParseIssueFields(issueTemplate, args.JiraFields, newSession)
			switch err.(type) {
			case *jira.ErrInvalidIssueFields:
				return nil, nil, fmt.Errorf("invalid jira issue fields: %v", err)
			case nil:
			default:
				return nil, nil, fmt.Errorf("failed parsing jira issue fields: %v", err)
			}
			resp, err := jira.CreateCustomerRequest(issueTemplate, jiraConfig, jiraFields)
			if err != nil {
				return nil, nil, fmt.Errorf("failed creating jira customer request: %v", err)
			}
			newSession.IntegrationsMetadata = map[string]any{
				"jira_issue_key": resp.IssueKey,
				"jira_issue_url": resp.Links.Agent,
			}
		}
	}

	if needsAiReview {
		if aiAccessRule == nil {
			return nil, nil, fmt.Errorf("ai analyzer requested review without resolving access request rule")
		}
		review, err := sessionapi.CreateReviewFromAIAnalysis(orgID, sessionID, conn,
			sessionapi.AIReviewRequester{
				UserID:      sc.UserID,
				UserEmail:   sc.UserEmail,
				UserName:    sc.UserName,
				UserSlackID: sc.SlackID,
				UserGroups:  sc.UserGroups,
			},
			aiAccessRule, args.Input, args.EnvVars, args.Args)
		if err != nil {
			return nil, nil, fmt.Errorf("failed creating ai-driven review: %v", err)
		}
		events.DeriveFromSessionStart(sc.OrgID, &newSession, conn)
		return execResponseToEnvelope(&clientexec.Response{
			HasReview:  true,
			Output:     fmt.Sprintf("%s/reviews/%s", appconfig.Get().FullApiURL(), review.ID),
			SessionID:  sessionID,
			AIAnalysis: sessionapi.ToOpenApiSessionAIAnalysis(analyzeRes),
		}, sessionID)
	}

	if err := services.CreateSession(nil, newSession, conn); err != nil {
		log.Errorf("failed creating session, err=%v", err)

		if errors.Is(err, services.ErrMissingMetadata) {
			return nil, nil, fmt.Errorf("missing metadata: %v", err)
		}

		return nil, nil, fmt.Errorf("failed creating session: %v", err)
	}
	trackClient.TrackSessionUsageData(analytics.EventSessionCreated, sc.GetOrgID(), sc.GetUserID(), sessionID)

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), execTimeout)
	defer cancelFn()

	resp, err := services.Exec(timeoutCtx, services.ExecOptions{
		OrgID:          sc.GetOrgID(),
		SessionID:      sessionID,
		ConnectionName: conn.Name,
		BearerToken:    token,
		UserAgent:      fmt.Sprintf("mcp.exec/%s", sc.UserEmail),
		Origin:         pb.ConnectionOriginClientAPI,
		Script:         args.Input,
		EnvVars:        args.EnvVars,
		ClientArgs:     args.Args,
	})
	switch {
	case err == nil:
		return execResponseToEnvelope(resp, sessionID)
	case errors.Is(err, context.DeadlineExceeded):
		log.With("sid", sessionID).Infof("mcp exec timeout (%s), returning status=running", execTimeout)
		return jsonResult(map[string]any{
			"status":     "running",
			"session_id": sessionID,
			"message":    fmt.Sprintf("execution still running after %s; poll sessions_get with session_id", execTimeout),
			"next_step":  "poll sessions_get with session_id",
		})
	default:
		return nil, nil, fmt.Errorf("failed creating exec client: %w", err)
	}
}

func execResponseToEnvelope(resp *clientexec.Response, sessionID string) (*mcp.CallToolResult, any, error) {
	if resp.HasReview {
		// Output is the review URI; the review_id is the session id.
		return jsonResult(map[string]any{
			"status":     "pending_approval",
			"session_id": sessionID,
			"review_id":  sessionID,
			"review_url": resp.Output,
			"message":    "Approval required before this execution can run",
			"next_step":  "call reviews_wait with review_id (long-polls until status changes); once status=APPROVED call reviews_execute",
		})
	}

	env := map[string]any{
		"session_id":        resp.SessionID,
		"output":            resp.Output,
		"output_status":     resp.OutputStatus,
		"truncated":         resp.Truncated,
		"execution_time_ms": resp.ExecutionTimeMili,
		"exit_code":         resp.ExitCode,
		"has_review":        resp.HasReview,
	}
	if resp.OutputStatus == "failed" || (resp.ExitCode != 0 && resp.ExitCode != -2) {
		env["status"] = "failed"
	} else {
		env["status"] = "completed"
	}
	if resp.AIAnalysis != nil {
		env["ai_analysis"] = map[string]any{
			"risk_level":  resp.AIAnalysis.RiskLevel,
			"title":       resp.AIAnalysis.Title,
			"explanation": resp.AIAnalysis.Explanation,
			"action":      resp.AIAnalysis.Action,
		}
	}
	return jsonResult(env)
}
