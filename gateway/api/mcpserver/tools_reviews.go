package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	reviewapi "github.com/hoophq/hoop/gateway/api/review"
	"github.com/hoophq/hoop/gateway/clientexec"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type reviewsListInput struct{}

type reviewsGetInput struct {
	ID string `json:"id" jsonschema:"review ID or session ID"`
}

type reviewTimeWindowInput struct {
	Type          string            `json:"type" jsonschema:"time window type (e.g. time_range)"`
	Configuration map[string]string `json:"configuration" jsonschema:"time window configuration with start_time and end_time in HH:MM format"`
}

type reviewsUpdateInput struct {
	ID          string                 `json:"id" jsonschema:"review ID or session ID"`
	Status      string                 `json:"status" jsonschema:"new status: APPROVED, REJECTED, or REVOKED"`
	TimeWindow  *reviewTimeWindowInput `json:"time_window,omitempty" jsonschema:"optional time window for approved JIT reviews"`
	ForceReview bool                   `json:"force_review,omitempty" jsonschema:"force the review (requires force approval group membership)"`
}

type reviewsExecuteInput struct {
	ID string `json:"id" jsonschema:"review ID or session ID of an APPROVED one-time review"`
}

func registerReviewTools(server *mcp.Server, releaseConnFn reviewapi.TransportReleaseConnectionFunc) {
	openWorld := false

	mcp.AddTool(server, &mcp.Tool{
		Name:        "reviews_list",
		Description: "List all reviews (access requests) for the organization",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, reviewsListHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "reviews_get",
		Description: "Get a single review by its ID or session ID",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, reviewsGetHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "reviews_update",
		Description: "Update a review status (approve, reject, or revoke). Requires membership in a reviewer group",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: &openWorld},
	}, makeReviewsUpdateHandler(releaseConnFn))

	mcp.AddTool(server, &mcp.Tool{
		Name: "reviews_execute",
		Description: "Execute a query that was blocked on an approved one-time review. Runs the originally " +
			"submitted query (no drift from what the reviewer approved). Only the session owner or an " +
			"admin/auditor can execute; the review must be in status=APPROVED. Returns the same envelope " +
			"shape as exec: completed, or status=running after a 50s timeout (poll sessions_get).",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: &openWorld},
	}, reviewsExecuteHandler)
}

// Per-session lock to prevent concurrent executions of the same review.
// This mirrors the HTTP handler's behavior but is scoped to the MCP tool.
var (
	reviewsExecMu   sync.Mutex
	reviewsExecLock = map[string]struct{}{}
)

func reviewsExecuteHandler(ctx context.Context, _ *mcp.CallToolRequest, args reviewsExecuteInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}
	token := accessTokenFrom(ctx)
	if token == "" {
		return errResult("unauthorized: missing access token"), nil, nil
	}
	if args.ID == "" {
		return errResult("id is required"), nil, nil
	}

	review, err := models.GetReviewByIdOrSid(sc.GetOrgID(), args.ID)
	switch err {
	case models.ErrNotFound:
		return errResult("review not found"), nil, nil
	case nil:
	default:
		return nil, nil, fmt.Errorf("failed retrieving review: %w", err)
	}
	if review == nil {
		return errResult("review not found"), nil, nil
	}
	if review.Type != models.ReviewTypeOneTime {
		return errResult("review is not a one-time review"), nil, nil
	}

	sid := review.SessionID
	reviewsExecMu.Lock()
	if _, busy := reviewsExecLock[sid]; busy {
		reviewsExecMu.Unlock()
		return errResult(fmt.Sprintf("the session %v is already being processed", sid)), nil, nil
	}
	reviewsExecLock[sid] = struct{}{}
	reviewsExecMu.Unlock()
	defer func() {
		reviewsExecMu.Lock()
		delete(reviewsExecLock, sid)
		reviewsExecMu.Unlock()
	}()

	session, err := models.GetSessionByID(sc.OrgID, sid)
	switch err {
	case models.ErrNotFound:
		return errResult("session not found"), nil, nil
	case nil:
	default:
		return nil, nil, fmt.Errorf("failed fetching session: %w", err)
	}
	session.BlobInput, err = session.GetBlobInput()
	if err != nil {
		return nil, nil, fmt.Errorf("failed fetching session input: %w", err)
	}

	if err := canExecReviewedSessionMCP(sc, session, review); err != nil {
		return errResult(err.Error()), nil, nil
	}

	client, err := clientexec.New(&clientexec.Options{
		OrgID:          sc.GetOrgID(),
		SessionID:      session.ID,
		ConnectionName: session.Connection,
		BearerToken:    token,
		UserAgent:      fmt.Sprintf("mcp.reviews_execute/%s", sc.UserEmail),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed creating exec client: %w", err)
	}

	respCh := make(chan *clientexec.Response, 1)
	go func() {
		defer func() { close(respCh); client.Close() }()
		respCh <- client.Run([]byte(session.BlobInput), review.InputEnvVars, review.InputClientArgs...)
	}()

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancelFn()

	reviewStatus := models.ReviewStatusExecuted
	defer func() {
		if err := models.UpdateReviewStatus(review.OrgID, review.ID, reviewStatus); err != nil {
			log.With("sid", sid).Warnf("failed updating review to status=%v, err=%v", reviewStatus, err)
		}
	}()

	select {
	case resp := <-respCh:
		env := map[string]any{
			"session_id":        resp.SessionID,
			"output":            resp.Output,
			"output_status":     resp.OutputStatus,
			"truncated":         resp.Truncated,
			"execution_time_ms": resp.ExecutionTimeMili,
			"exit_code":         resp.ExitCode,
		}
		if resp.OutputStatus == "failed" || (resp.ExitCode != 0 && resp.ExitCode != -2) {
			env["status"] = "failed"
		} else {
			env["status"] = "completed"
		}
		return jsonResult(env)
	case <-timeoutCtx.Done():
		client.Close()
		reviewStatus = models.ReviewStatusUnknown
		return jsonResult(map[string]any{
			"session_id": session.ID,
			"status":     "running",
			"message":    "execution still running after 50s; poll sessions_get with session_id",
			"next_step":  "poll sessions_get with session_id",
		})
	}
}

// canExecReviewedSessionMCP enforces the same rules as the HTTP handler's
// canExecReviewedSession: only the session owner or an admin/auditor may run
// the approved review, and the time window (if any) must currently permit it.
func canExecReviewedSessionMCP(ctx *storagev2.Context, session *models.Session, review *models.Review) error {
	if session.UserEmail != ctx.UserEmail && !ctx.IsAuditorOrAdminUser() {
		return fmt.Errorf("unable to execute session")
	}
	if review.Status != models.ReviewStatusApproved {
		return fmt.Errorf("review not approved or already executed")
	}
	if review.TimeWindow == nil {
		return nil
	}
	if review.TimeWindow.Type != "time_range" {
		return fmt.Errorf("unknown execution window type %s", review.TimeWindow.Type)
	}
	startStr, okStart := review.TimeWindow.Configuration["start_time"]
	endStr, okEnd := review.TimeWindow.Configuration["end_time"]
	if !okStart || !okEnd {
		return fmt.Errorf("invalid execution window configuration")
	}
	startTime, err := time.Parse("15:04", startStr)
	if err != nil {
		return fmt.Errorf("invalid execution window start time")
	}
	endTime, err := time.Parse("15:04", endStr)
	if err != nil {
		return fmt.Errorf("invalid execution window end time")
	}
	if endTime.Before(startTime) {
		endTime = endTime.Add(24 * time.Hour)
	}
	now := time.Now().UTC()
	nowOnlyTime := time.Date(0, 1, 1, now.Hour(), now.Minute(), now.Second(), now.Nanosecond(), time.UTC)
	if nowOnlyTime.Before(startTime) || nowOnlyTime.After(endTime) {
		return fmt.Errorf("execution not allowed outside the time window %s to %s UTC", startStr, endStr)
	}
	return nil
}

func reviewsListHandler(ctx context.Context, _ *mcp.CallToolRequest, _ reviewsListInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	reviews, err := models.ListReviews(sc.GetOrgID())
	if err != nil {
		return nil, nil, fmt.Errorf("failed listing reviews: %w", err)
	}

	result := make([]map[string]any, 0, len(*reviews))
	for _, r := range *reviews {
		result = append(result, reviewToMap(&r))
	}
	return jsonResult(result)
}

func reviewsGetHandler(ctx context.Context, _ *mcp.CallToolRequest, args reviewsGetInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	review, err := models.GetReviewByIdOrSid(sc.GetOrgID(), args.ID)
	switch err {
	case models.ErrNotFound:
		return errResult("review not found"), nil, nil
	case nil:
		return jsonResult(reviewToMap(review))
	default:
		return nil, nil, fmt.Errorf("failed fetching review: %w", err)
	}
}

func makeReviewsUpdateHandler(releaseConnFn reviewapi.TransportReleaseConnectionFunc) func(context.Context, *mcp.CallToolRequest, reviewsUpdateInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args reviewsUpdateInput) (*mcp.CallToolResult, any, error) {
		sc := storageContextFrom(ctx)
		if sc == nil {
			return nil, nil, fmt.Errorf("unauthorized: missing auth context")
		}

		status := models.ReviewStatusType(strings.ToUpper(args.Status))

		// Parse time window if provided
		var reviewTimeWindow *models.ReviewTimeWindow
		if args.TimeWindow != nil {
			openapiTW := &openapi.ReviewSessionTimeWindow{
				Type:          openapi.ReviewTimeWindowType(args.TimeWindow.Type),
				Configuration: args.TimeWindow.Configuration,
			}
			tw, err := reviewapi.ParseTimeWindow(openapiTW)
			if err != nil {
				return errResult(err.Error()), nil, nil
			}
			reviewTimeWindow = tw
		}

		rev, err := reviewapi.DoReview(sc, args.ID, status, reviewTimeWindow, args.ForceReview)
		switch err {
		case reviewapi.ErrNotEligible, reviewapi.ErrSelfApproval, reviewapi.ErrWrongState:
			return errResult(err.Error()), nil, nil
		case reviewapi.ErrForbidden:
			return errResult("access denied"), nil, nil
		case reviewapi.ErrNotFound:
			return errResult("review not found"), nil, nil
		case nil:
			// Release transport connection if review was approved or rejected
			if rev.Status == models.ReviewStatusApproved || rev.Status == models.ReviewStatusRejected {
				if releaseConnFn != nil {
					releaseConnFn(
						rev.OrgID,
						rev.SessionID,
						ptr.ToString(rev.OwnerSlackID),
						rev.Status.Str(),
					)
				} else {
					log.Warnf("mcp: review update succeeded but transport release function is nil, sid=%v", rev.SessionID)
				}
			}
			return jsonResult(reviewToMap(rev))
		default:
			return nil, nil, fmt.Errorf("failed updating review: %w", err)
		}
	}
}

func reviewToMap(r *models.Review) map[string]any {
	m := map[string]any{
		"id":         r.ID,
		"session":    r.SessionID,
		"type":       string(r.Type),
		"status":     string(r.Status),
		"created_at": r.CreatedAt,
	}

	if r.AccessDurationSec > 0 {
		m["access_duration"] = time.Duration(r.AccessDurationSec) * time.Second
	}
	if r.RevokedAt != nil {
		m["revoke_at"] = r.RevokedAt
	}
	if r.AccessRequestRuleName != nil {
		m["access_request_rule_name"] = *r.AccessRequestRuleName
	}
	if r.MinApprovals != nil {
		m["min_approvals"] = *r.MinApprovals
	}
	if len(r.ForceApprovalGroups) > 0 {
		m["force_approval_groups"] = []string(r.ForceApprovalGroups)
	}
	if r.ConnectionName != "" {
		m["connection_name"] = r.ConnectionName
	}
	if r.OwnerEmail != "" {
		m["owner_email"] = r.OwnerEmail
	}

	if r.TimeWindow != nil {
		m["time_window"] = map[string]any{
			"type":          r.TimeWindow.Type,
			"configuration": r.TimeWindow.Configuration,
		}
	}

	if len(r.ReviewGroups) > 0 {
		groups := make([]map[string]any, 0, len(r.ReviewGroups))
		for _, rg := range r.ReviewGroups {
			g := map[string]any{
				"id":     rg.ID,
				"group":  rg.GroupName,
				"status": string(rg.Status),
			}
			if rg.OwnerID != nil {
				g["reviewed_by"] = map[string]any{
					"id":    *rg.OwnerID,
					"name":  ptr.ToString(rg.OwnerName),
					"email": ptr.ToString(rg.OwnerEmail),
				}
			}
			if rg.ReviewedAt != nil {
				g["review_date"] = rg.ReviewedAt
			}
			if rg.ForcedReview {
				g["forced_review"] = true
			}
			groups = append(groups, g)
		}
		m["review_groups_data"] = groups
	}

	return m
}
