package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	reviewapi "github.com/hoophq/hoop/gateway/api/review"
	"github.com/hoophq/hoop/gateway/models"
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
