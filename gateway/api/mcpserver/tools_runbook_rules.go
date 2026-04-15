package mcpserver

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type runbookRuleFileInput struct {
	Repository string `json:"repository" jsonschema:"git URL of the runbook repository"`
	Name       string `json:"name" jsonschema:"file path within the repository"`
}

type runbookRulesListInput struct{}

type runbookRulesGetInput struct {
	ID string `json:"id" jsonschema:"runbook rule ID"`
}

type runbookRulesCreateInput struct {
	Name        string                 `json:"name" jsonschema:"unique rule name"`
	Description string                 `json:"description,omitempty" jsonschema:"human-readable description"`
	UserGroups  []string               `json:"user_groups,omitempty" jsonschema:"user groups that can use these runbooks"`
	Connections []string               `json:"connections,omitempty" jsonschema:"connection names this rule applies to"`
	Runbooks    []runbookRuleFileInput `json:"runbooks,omitempty" jsonschema:"list of runbooks allowed by this rule"`
}

type runbookRulesUpdateInput struct {
	ID          string                 `json:"id" jsonschema:"runbook rule ID to update"`
	Name        string                 `json:"name,omitempty" jsonschema:"rule name"`
	Description string                 `json:"description,omitempty" jsonschema:"human-readable description"`
	UserGroups  []string               `json:"user_groups,omitempty" jsonschema:"user groups that can use these runbooks"`
	Connections []string               `json:"connections,omitempty" jsonschema:"connection names this rule applies to"`
	Runbooks    []runbookRuleFileInput `json:"runbooks,omitempty" jsonschema:"list of runbooks allowed by this rule"`
}

type runbookRulesDeleteInput struct {
	ID string `json:"id" jsonschema:"runbook rule ID to delete"`
}

func registerRunbookRuleTools(server *mcp.Server) {
	openWorld := false

	mcp.AddTool(server, &mcp.Tool{
		Name:        "runbook_rules_list",
		Description: "List all runbook rules configured for the organization",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, runbookRulesListHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "runbook_rules_get",
		Description: "Get a single runbook rule by its ID",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, runbookRulesGetHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "runbook_rules_create",
		Description: "Create a new runbook rule with user groups, connections, and allowed runbooks",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: &openWorld},
	}, runbookRulesCreateHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "runbook_rules_update",
		Description: "Update an existing runbook rule by ID",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: &openWorld},
	}, runbookRulesUpdateHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "runbook_rules_delete",
		Description: "Delete a runbook rule by ID. This action is irreversible",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: &openWorld},
	}, runbookRulesDeleteHandler)
}

func runbookRulesListHandler(ctx context.Context, _ *mcp.CallToolRequest, _ runbookRulesListInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	rules, err := models.GetRunbookRules(models.DB, sc.GetOrgID(), 0, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("failed listing runbook rules: %w", err)
	}

	result := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		result = append(result, runbookRuleToMap(&rule))
	}
	return jsonResult(result)
}

func runbookRulesGetHandler(ctx context.Context, _ *mcp.CallToolRequest, args runbookRulesGetInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	rule, err := models.GetRunbookRuleByID(models.DB, sc.GetOrgID(), args.ID)
	if err != nil {
		return errResult("runbook rule not found"), nil, nil
	}

	return jsonResult(runbookRuleToMap(rule))
}

func runbookRulesCreateHandler(ctx context.Context, _ *mcp.CallToolRequest, args runbookRulesCreateInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	rule := &models.RunbookRules{
		ID:          uuid.NewString(),
		OrgID:       sc.GetOrgID(),
		Name:        args.Name,
		Description: sql.NullString{String: args.Description, Valid: args.Description != ""},
		UserGroups:  args.UserGroups,
		Connections: args.Connections,
		Runbooks:    toRunbookRuleFiles(args.Runbooks),
	}

	if err := models.UpsertRunbookRule(models.DB, rule); err != nil {
		return nil, nil, fmt.Errorf("failed creating runbook rule: %w", err)
	}

	return jsonResult(runbookRuleToMap(rule))
}

func runbookRulesUpdateHandler(ctx context.Context, _ *mcp.CallToolRequest, args runbookRulesUpdateInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	existing, err := models.GetRunbookRuleByID(models.DB, sc.GetOrgID(), args.ID)
	if err != nil {
		return errResult("runbook rule not found"), nil, nil
	}

	if args.Name != "" {
		existing.Name = args.Name
	}
	if args.Description != "" {
		existing.Description = sql.NullString{String: args.Description, Valid: true}
	}
	if args.UserGroups != nil {
		existing.UserGroups = args.UserGroups
	}
	if args.Connections != nil {
		existing.Connections = args.Connections
	}
	if args.Runbooks != nil {
		existing.Runbooks = toRunbookRuleFiles(args.Runbooks)
	}

	if err := models.UpsertRunbookRule(models.DB, existing); err != nil {
		return nil, nil, fmt.Errorf("failed updating runbook rule: %w", err)
	}

	return jsonResult(runbookRuleToMap(existing))
}

func runbookRulesDeleteHandler(ctx context.Context, _ *mcp.CallToolRequest, args runbookRulesDeleteInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	if err := models.DeleteRunbookRule(models.DB, sc.GetOrgID(), args.ID); err != nil {
		return errResult("runbook rule not found"), nil, nil
	}

	return textResult(fmt.Sprintf("runbook rule %q deleted successfully", args.ID)), nil, nil
}

func runbookRuleToMap(rule *models.RunbookRules) map[string]any {
	m := map[string]any{
		"id":         rule.ID,
		"name":       rule.Name,
		"created_at": rule.CreatedAt,
		"updated_at": rule.UpdatedAt,
	}
	if rule.Description.Valid {
		m["description"] = rule.Description.String
	}
	if len(rule.UserGroups) > 0 {
		m["user_groups"] = rule.UserGroups
	}
	if len(rule.Connections) > 0 {
		m["connections"] = rule.Connections
	}
	if len(rule.Runbooks) > 0 {
		runbooks := make([]map[string]string, 0, len(rule.Runbooks))
		for _, rb := range rule.Runbooks {
			runbooks = append(runbooks, map[string]string{
				"repository": rb.Repository,
				"name":       rb.Name,
			})
		}
		m["runbooks"] = runbooks
	}
	return m
}

func toRunbookRuleFiles(input []runbookRuleFileInput) models.RunbookRuleFiles {
	files := make(models.RunbookRuleFiles, 0, len(input))
	for _, f := range input {
		files = append(files, models.RunbookRuleFile{
			Repository: f.Repository,
			Name:       f.Name,
		})
	}
	return files
}
