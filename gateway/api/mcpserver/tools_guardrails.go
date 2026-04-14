package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type guardrailsListInput struct{}

type guardrailsGetInput struct {
	ID string `json:"id" jsonschema:"guardrail rule ID or name"`
}

type guardrailsCreateInput struct {
	Name          string         `json:"name" jsonschema:"unique guardrail rule name"`
	Description   string         `json:"description,omitempty" jsonschema:"human-readable description"`
	Input         map[string]any `json:"input,omitempty" jsonschema:"input rule configuration"`
	Output        map[string]any `json:"output,omitempty" jsonschema:"output rule configuration"`
	ConnectionIDs []string       `json:"connection_ids,omitempty" jsonschema:"connection IDs to associate with this rule"`
}

type guardrailsUpdateInput struct {
	ID            string         `json:"id" jsonschema:"guardrail rule ID to update"`
	Name          string         `json:"name" jsonschema:"guardrail rule name"`
	Description   string         `json:"description,omitempty" jsonschema:"human-readable description"`
	Input         map[string]any `json:"input,omitempty" jsonschema:"input rule configuration"`
	Output        map[string]any `json:"output,omitempty" jsonschema:"output rule configuration"`
	ConnectionIDs []string       `json:"connection_ids,omitempty" jsonschema:"connection IDs to associate with this rule"`
}

type guardrailsDeleteInput struct {
	ID string `json:"id" jsonschema:"guardrail rule ID to delete"`
}

func registerGuardrailTools(server *mcp.Server) {
	openWorld := false

	mcp.AddTool(server, &mcp.Tool{
		Name:        "guardrails_list",
		Description: "List all guardrail rules configured for the organization",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, guardrailsListHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "guardrails_get",
		Description: "Get a single guardrail rule by its ID or name",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, guardrailsGetHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "guardrails_create",
		Description: "Create a new guardrail rule with input/output configurations and connection associations",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: &openWorld},
	}, guardrailsCreateHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "guardrails_update",
		Description: "Update an existing guardrail rule by ID",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: &openWorld},
	}, guardrailsUpdateHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "guardrails_delete",
		Description: "Delete a guardrail rule by ID. This action is irreversible",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: &openWorld},
	}, guardrailsDeleteHandler)
}

func guardrailsListHandler(ctx context.Context, _ *mcp.CallToolRequest, _ guardrailsListInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	rules, err := models.ListGuardRailRules(sc.GetOrgID())
	if err != nil {
		return nil, nil, fmt.Errorf("failed listing guardrail rules: %w", err)
	}

	result := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		result = append(result, guardrailToMap(rule))
	}
	return jsonResult(result)
}

func guardrailsGetHandler(ctx context.Context, _ *mcp.CallToolRequest, args guardrailsGetInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	rule, err := models.GetGuardRailRules(sc.GetOrgID(), args.ID)
	if err == models.ErrNotFound {
		return errResult("guardrail rule not found"), nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed fetching guardrail rule: %w", err)
	}

	return jsonResult(guardrailToMap(rule))
}

func guardrailsCreateHandler(ctx context.Context, _ *mcp.CallToolRequest, args guardrailsCreateInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	validConnectionIDs := filterEmptyStrings(args.ConnectionIDs)

	rule := &models.GuardRailRules{
		ID:          uuid.NewString(),
		OrgID:       sc.GetOrgID(),
		Name:        args.Name,
		Description: args.Description,
		Input:       args.Input,
		Output:      args.Output,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	err := models.UpsertGuardRailRuleWithConnections(rule, validConnectionIDs, true)
	if err == models.ErrAlreadyExists {
		return errResult("guardrail rule already exists"), nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed creating guardrail rule: %w", err)
	}

	rule.ConnectionIDs = validConnectionIDs
	return jsonResult(guardrailToMap(rule))
}

func guardrailsUpdateHandler(ctx context.Context, _ *mcp.CallToolRequest, args guardrailsUpdateInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	validConnectionIDs := filterEmptyStrings(args.ConnectionIDs)

	rule := &models.GuardRailRules{
		OrgID:       sc.GetOrgID(),
		ID:          args.ID,
		Name:        args.Name,
		Description: args.Description,
		Input:       args.Input,
		Output:      args.Output,
		UpdatedAt:   time.Now().UTC(),
	}

	err := models.UpsertGuardRailRuleWithConnections(rule, validConnectionIDs, false)
	if err == models.ErrNotFound {
		return errResult("guardrail rule not found"), nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed updating guardrail rule: %w", err)
	}

	rule.ConnectionIDs = validConnectionIDs
	return jsonResult(guardrailToMap(rule))
}

func guardrailsDeleteHandler(ctx context.Context, _ *mcp.CallToolRequest, args guardrailsDeleteInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	err := models.DeleteGuardRailRules(sc.GetOrgID(), args.ID)
	switch err {
	case models.ErrNotFound:
		return errResult("guardrail rule not found"), nil, nil
	case nil:
		return textResult(fmt.Sprintf("guardrail rule %q deleted successfully", args.ID)), nil, nil
	default:
		return nil, nil, fmt.Errorf("failed deleting guardrail rule: %w", err)
	}
}

func guardrailToMap(rule *models.GuardRailRules) map[string]any {
	m := map[string]any{
		"id":          rule.ID,
		"name":        rule.Name,
		"description": rule.Description,
		"created_at":  rule.CreatedAt,
		"updated_at":  rule.UpdatedAt,
	}
	if rule.Input != nil {
		m["input"] = rule.Input
	}
	if rule.Output != nil {
		m["output"] = rule.Output
	}
	if len(rule.ConnectionIDs) > 0 {
		m["connection_ids"] = rule.ConnectionIDs
	}
	if len(rule.Attributes) > 0 {
		m["attributes"] = rule.Attributes
	}
	return m
}

func filterEmptyStrings(ids []string) []string {
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" {
			result = append(result, id)
		}
	}
	return result
}
