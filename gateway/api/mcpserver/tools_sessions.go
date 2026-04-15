package mcpserver

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const defaultMaxSessionListLimit = 100

type sessionsListInput struct {
	User         string `json:"user,omitempty" jsonschema:"filter by user email"`
	Connection   string `json:"connection,omitempty" jsonschema:"filter by connection name"`
	Type         string `json:"type,omitempty" jsonschema:"filter by connection type (database, application, custom)"`
	ReviewStatus string `json:"review_status,omitempty" jsonschema:"filter by review status (PENDING, APPROVED, REJECTED)"`
	StartDate    string `json:"start_date,omitempty" jsonschema:"start of date range in RFC3339 format"`
	EndDate      string `json:"end_date,omitempty" jsonschema:"end of date range in RFC3339 format"`
	Limit        int    `json:"limit,omitempty" jsonschema:"max results (default 20, max 100)"`
	Offset       int    `json:"offset,omitempty" jsonschema:"pagination offset (default 0)"`
}

type sessionsGetInput struct {
	ID string `json:"id" jsonschema:"session ID"`
}

func registerSessionTools(server *mcp.Server) {
	openWorld := false

	mcp.AddTool(server, &mcp.Tool{
		Name:        "sessions_list",
		Description: "List sessions with optional filters (user, connection, date range, status). Returns metadata only, not session content. Max 100 results per request",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, sessionsListHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "sessions_get",
		Description: "Get session details and metadata by ID. Returns metadata only, not session content or binary data",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, sessionsGetHandler)
}

func sessionsListHandler(ctx context.Context, _ *mcp.CallToolRequest, args sessionsListInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	option := models.NewSessionOption()

	if args.User != "" {
		option.User = args.User
	}
	if args.Connection != "" {
		option.ConnectionName = args.Connection
	}
	if args.Type != "" {
		option.ConnectionType = args.Type
	}
	if args.ReviewStatus != "" {
		option.ReviewStatus = args.ReviewStatus
	}
	if args.Limit > 0 {
		option.Limit = args.Limit
	}
	if args.Offset > 0 {
		option.Offset = args.Offset
	}

	if args.StartDate != "" {
		t, err := time.Parse(time.RFC3339, args.StartDate)
		if err != nil {
			return errResult("invalid start_date format, expected RFC3339 (e.g. 2024-01-01T00:00:00Z)"), nil, nil
		}
		option.StartDate = sql.NullString{String: t.Format(time.RFC3339), Valid: true}
	}
	if args.EndDate != "" {
		t, err := time.Parse(time.RFC3339, args.EndDate)
		if err != nil {
			return errResult("invalid end_date format, expected RFC3339 (e.g. 2024-12-31T23:59:59Z)"), nil, nil
		}
		option.EndDate = sql.NullString{String: t.Format(time.RFC3339), Valid: true}
	}

	if option.Limit > defaultMaxSessionListLimit {
		option.Limit = defaultMaxSessionListLimit
	}

	// If start_date is set but end_date is not, default to now
	if option.StartDate.Valid && !option.EndDate.Valid {
		option.EndDate = sql.NullString{
			String: time.Now().UTC().Format(time.RFC3339),
			Valid:  true,
		}
	}

	sessionList, err := models.ListSessions(sc.OrgID, sc.UserID, sc.IsAuditorOrAdminUser(), option)
	if err != nil {
		return nil, nil, fmt.Errorf("failed listing sessions: %w", err)
	}

	items := make([]map[string]any, 0, len(sessionList.Items))
	for _, s := range sessionList.Items {
		items = append(items, sessionToMap(&s))
	}

	result := map[string]any{
		"total":         sessionList.Total,
		"has_next_page": sessionList.HasNextPage,
		"items":         items,
	}
	return jsonResult(result)
}

func sessionsGetHandler(ctx context.Context, _ *mcp.CallToolRequest, args sessionsGetInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	session, err := models.GetSessionByID(sc.OrgID, args.ID)
	switch err {
	case models.ErrNotFound:
		return errResult("session not found"), nil, nil
	case nil:
	default:
		return nil, nil, fmt.Errorf("failed fetching session: %w", err)
	}

	// Access check: user must own the session or be admin/auditor
	if session.UserID != sc.UserID && !sc.IsAuditorOrAdminUser() {
		return errResult("access denied: you can only view your own sessions or must be admin/auditor"), nil, nil
	}

	return jsonResult(sessionToMap(session))
}

func sessionToMap(s *models.Session) map[string]any {
	m := map[string]any{
		"id":                 s.ID,
		"connection":         s.Connection,
		"connection_type":    s.ConnectionType,
		"connection_subtype": s.ConnectionSubtype,
		"user_id":            s.UserID,
		"user_email":         s.UserEmail,
		"user_name":          s.UserName,
		"status":             s.Status,
		"verb":               s.Verb,
		"created_at":         s.CreatedAt,
	}

	if s.EndSession != nil {
		m["ended_at"] = s.EndSession
	}
	if s.ExitCode != nil {
		m["exit_code"] = *s.ExitCode
	}
	if s.SessionBatchID != nil {
		m["session_batch_id"] = *s.SessionBatchID
	}
	if len(s.Labels) > 0 {
		m["labels"] = s.Labels
	}
	if len(s.Metadata) > 0 {
		m["metadata"] = s.Metadata
	}
	if len(s.IntegrationsMetadata) > 0 {
		m["integrations_metadata"] = s.IntegrationsMetadata
	}
	if len(s.Metrics) > 0 {
		m["metrics"] = s.Metrics
	}
	if len(s.ConnectionTags) > 0 {
		m["connection_tags"] = s.ConnectionTags
	}

	if s.Review != nil {
		review := map[string]any{
			"id":     s.Review.ID,
			"type":   s.Review.Type,
			"status": s.Review.Status,
		}
		if s.Review.AccessDurationSec > 0 {
			review["access_duration"] = time.Duration(s.Review.AccessDurationSec) * time.Second
		}
		if s.Review.AccessRequestRuleName != nil {
			review["access_request_rule_name"] = *s.Review.AccessRequestRuleName
		}
		m["review"] = review
	}

	if s.AIAnalysis != nil {
		m["ai_analysis"] = map[string]any{
			"risk_level":  s.AIAnalysis.RiskLevel,
			"title":       s.AIAnalysis.Title,
			"explanation": s.AIAnalysis.Explanation,
			"action":      s.AIAnalysis.Action,
		}
	}

	if len(s.GuardRailsInfo) > 0 {
		grInfo := make([]map[string]any, 0, len(s.GuardRailsInfo))
		for _, gr := range s.GuardRailsInfo {
			grInfo = append(grInfo, map[string]any{
				"rule_name":     gr.RuleName,
				"direction":     gr.Direction,
				"matched_words": gr.MatchedWords,
			})
		}
		m["guardrails_info"] = grInfo
	}

	return m
}
