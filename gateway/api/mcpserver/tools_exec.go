package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/clientexec"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const execTimeout = 50 * time.Second

type execInput struct {
	ConnectionName string   `json:"connection_name" jsonschema:"name of the Hoop connection to execute against"`
	Input          string   `json:"input" jsonschema:"the query or command to run (e.g. a SQL statement for a database connection)"`
	Args           []string `json:"args,omitempty" jsonschema:"optional client arguments forwarded to the connection (e.g. CLI flags)"`
	EnvVars        map[string]string `json:"env_vars,omitempty" jsonschema:"optional environment variables forwarded to the connection"`
}

func registerExecTools(server *mcp.Server) {
	openWorld := false

	mcp.AddTool(server, &mcp.Tool{
		Name: "exec",
		Description: "Run a one-shot command or query against a Hoop connection on behalf of the authenticated user. " +
			"Mirrors `hoop exec`. Returns one of three envelopes: `status=completed` with output, " +
			"`status=pending_approval` with a review_id (poll reviews_get; once APPROVED call reviews_execute), " +
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

	// Origin=client (matches `hoop exec` CLI) triggers full audit persistence:
	// the audit plugin inserts the session row on OnConnect, stores the query
	// via UpdateSessionInput, and runs AI analysis on OnReceive. Origin=client-api
	// is reserved for HTTP flows that pre-insert the session row themselves.
	sessionID := uuid.NewString()
	client, err := clientexec.New(&clientexec.Options{
		OrgID:          sc.GetOrgID(),
		SessionID:      sessionID,
		ConnectionName: conn.Name,
		BearerToken:    token,
		UserAgent:      fmt.Sprintf("mcp.exec/%s", sc.UserEmail),
		Origin:         pb.ConnectionOriginClient,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed creating exec client: %w", err)
	}

	respCh := make(chan *clientexec.Response, 1)
	go func() {
		defer func() { close(respCh); client.Close() }()
		respCh <- client.Run([]byte(args.Input), args.EnvVars, args.Args...)
	}()

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), execTimeout)
	defer cancelFn()

	select {
	case resp := <-respCh:
		return execResponseToEnvelope(resp, sessionID)
	case <-timeoutCtx.Done():
		// Force the goroutine to unblock; its result will be persisted async.
		client.Close()
		log.With("sid", sessionID).Infof("mcp exec timeout (%s), returning status=running", execTimeout)
		return jsonResult(map[string]any{
			"status":     "running",
			"session_id": sessionID,
			"message":    fmt.Sprintf("execution still running after %s; poll sessions_get with session_id", execTimeout),
			"next_step":  "poll sessions_get with session_id",
		})
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
			"next_step":  "poll reviews_get with review_id; once status=APPROVED call reviews_execute",
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
