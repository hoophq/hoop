package mcpserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gorm.io/gorm"
)

type attributesListInput struct {
	Search   string `json:"search,omitempty" jsonschema:"optional name search (ILIKE substring match)"`
	Page     int    `json:"page,omitempty" jsonschema:"page number, 1-based (default 1 when page_size is set)"`
	PageSize int    `json:"page_size,omitempty" jsonschema:"items per page (default: return all)"`
}

type attributesGetInput struct {
	Name string `json:"name" jsonschema:"attribute name"`
}

type attributesCreateInput struct {
	Name                    string   `json:"name" jsonschema:"unique attribute name"`
	Description             *string  `json:"description,omitempty" jsonschema:"human-readable description"`
	ConnectionNames         []string `json:"connection_names,omitempty" jsonschema:"connections to associate with this attribute"`
	AccessRequestRuleNames  []string `json:"access_request_rule_names,omitempty" jsonschema:"access request rules to associate with this attribute"`
	GuardrailRuleNames      []string `json:"guardrail_rule_names,omitempty" jsonschema:"guardrail rules to associate with this attribute"`
	DatamaskingRuleNames    []string `json:"datamasking_rule_names,omitempty" jsonschema:"data masking rules to associate with this attribute"`
	AccessControlGroupNames []string `json:"access_control_group_names,omitempty" jsonschema:"user groups to associate with this attribute via the access_control plugin"`
}

type attributesUpdateInput struct {
	Name                    string   `json:"name" jsonschema:"attribute name to update"`
	Description             *string  `json:"description,omitempty" jsonschema:"human-readable description"`
	ConnectionNames         []string `json:"connection_names,omitempty" jsonschema:"connections to associate. Omit to leave unchanged; pass an empty array to clear all connection associations."`
	AccessRequestRuleNames  []string `json:"access_request_rule_names,omitempty" jsonschema:"access request rules to associate. Omit to leave unchanged; pass an empty array to clear."`
	GuardrailRuleNames      []string `json:"guardrail_rule_names,omitempty" jsonschema:"guardrail rules to associate. Omit to leave unchanged; pass an empty array to clear."`
	DatamaskingRuleNames    []string `json:"datamasking_rule_names,omitempty" jsonschema:"data masking rules to associate. Omit to leave unchanged; pass an empty array to clear."`
	AccessControlGroupNames []string `json:"access_control_group_names,omitempty" jsonschema:"access_control group bindings. Omit to leave unchanged; pass an empty array to clear."`
}

type attributesDeleteInput struct {
	Name string `json:"name" jsonschema:"attribute name to delete"`
}

func registerAttributeTools(server *mcp.Server) {
	openWorld := false

	mcp.AddTool(server, &mcp.Tool{
		Name: "attributes_list",
		Description: "List attributes for the organization. Attributes are tags that group connections, access-request/guardrail/data-masking rules, and access-control groups. " +
			"Supports optional pagination (page/page_size) and substring search on name.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, attributesListHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "attributes_get",
		Description: "Get a single attribute by name, including all of its associations (connections, rules, access-control groups).",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, attributesGetHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name: "attributes_create",
		Description: "Create a new attribute, optionally associating it with connections, rules, and access-control groups in the same call. Requires admin access.",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: &openWorld},
	}, attributesCreateHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name: "attributes_update",
		Description: "Update an attribute's description and/or its associations. Requires admin access. " +
			"Each association field uses preserve-on-omit semantics: omit a field to leave it unchanged, pass an empty array to clear all entries for that field. " +
			"To partially modify a list (add or remove a single entry), call attributes_get first, mutate the list, then pass the full updated list back.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: &openWorld},
	}, attributesUpdateHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "attributes_delete",
		Description: "Delete an attribute by name. Requires admin access. This action is irreversible and removes all of the attribute's associations.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: &openWorld},
	}, attributesDeleteHandler)
}

func attributesListHandler(ctx context.Context, _ *mcp.CallToolRequest, args attributesListInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}
	if !sc.IsAdminUser() {
		return errResult("admin access required"), nil, nil
	}

	orgID, err := uuid.Parse(sc.GetOrgID())
	if err != nil {
		return nil, nil, fmt.Errorf("invalid org id: %w", err)
	}

	opts := models.AttributeFilterOption{
		Search:   args.Search,
		Page:     args.Page,
		PageSize: args.PageSize,
	}
	if opts.PageSize > 0 && opts.Page <= 0 {
		opts.Page = 1
	}

	attrs, total, err := models.ListAttributes(models.DB, orgID, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("failed listing attributes: %w", err)
	}

	items := make([]map[string]any, 0, len(attrs))
	for _, a := range attrs {
		items = append(items, attributeToMap(a))
	}
	return jsonResult(map[string]any{
		"pages": map[string]any{
			"total": total,
			"page":  opts.Page,
			"size":  opts.PageSize,
		},
		"data": items,
	})
}

func attributesGetHandler(ctx context.Context, _ *mcp.CallToolRequest, args attributesGetInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}
	if !sc.IsAdminUser() {
		return errResult("admin access required"), nil, nil
	}
	if args.Name == "" {
		return errResult("name is required"), nil, nil
	}

	orgID, err := uuid.Parse(sc.GetOrgID())
	if err != nil {
		return nil, nil, fmt.Errorf("invalid org id: %w", err)
	}

	attr, err := models.GetAttribute(models.DB, orgID, args.Name)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errResult("attribute not found"), nil, nil
		}
		return nil, nil, fmt.Errorf("failed fetching attribute: %w", err)
	}
	return jsonResult(attributeToMap(attr))
}

func attributesCreateHandler(ctx context.Context, _ *mcp.CallToolRequest, args attributesCreateInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}
	if !sc.IsAdminUser() {
		return errResult("admin access required"), nil, nil
	}
	if err := apivalidation.ValidateResourceName(args.Name); err != nil {
		return errResult(err.Error()), nil, nil
	}

	orgID, err := uuid.Parse(sc.GetOrgID())
	if err != nil {
		return nil, nil, fmt.Errorf("invalid org id: %w", err)
	}

	attr := buildAttributeFromInput(orgID, args.Name, args.Description,
		args.ConnectionNames, args.AccessRequestRuleNames, args.GuardrailRuleNames,
		args.DatamaskingRuleNames, args.AccessControlGroupNames)

	if err := models.UpsertAttribute(models.DB, attr); err != nil {
		if errors.Is(err, models.ErrAlreadyExists) {
			return errResult("attribute already exists"), nil, nil
		}
		return nil, nil, fmt.Errorf("failed creating attribute: %w", err)
	}
	return jsonResult(attributeToMap(attr))
}

func attributesUpdateHandler(ctx context.Context, _ *mcp.CallToolRequest, args attributesUpdateInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}
	if !sc.IsAdminUser() {
		return errResult("admin access required"), nil, nil
	}
	if args.Name == "" {
		return errResult("name is required"), nil, nil
	}

	orgID, err := uuid.Parse(sc.GetOrgID())
	if err != nil {
		return nil, nil, fmt.Errorf("invalid org id: %w", err)
	}

	existing, err := models.GetAttribute(models.DB, orgID, args.Name)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errResult("attribute not found"), nil, nil
		}
		return nil, nil, fmt.Errorf("failed fetching attribute: %w", err)
	}

	// Description: pointer-based, nil means "leave unchanged".
	if args.Description != nil {
		existing.Description = args.Description
	}
	// Each association list: nil means "leave unchanged" (UpsertAttribute skips
	// nil junction lists). An empty (non-nil) slice means "clear all entries".
	if args.ConnectionNames != nil {
		existing.Connections = make([]models.ConnectionAttribute, len(args.ConnectionNames))
		for i, n := range args.ConnectionNames {
			existing.Connections[i] = models.ConnectionAttribute{OrgID: orgID, AttributeName: args.Name, ConnectionName: n}
		}
	} else {
		existing.Connections = nil
	}
	if args.AccessRequestRuleNames != nil {
		existing.AccessRequestRules = make([]models.AccessRequestRuleAttribute, len(args.AccessRequestRuleNames))
		for i, n := range args.AccessRequestRuleNames {
			existing.AccessRequestRules[i] = models.AccessRequestRuleAttribute{OrgID: orgID, AttributeName: args.Name, AccessRuleName: n}
		}
	} else {
		existing.AccessRequestRules = nil
	}
	if args.GuardrailRuleNames != nil {
		existing.GuardrailRules = make([]models.GuardrailRuleAttribute, len(args.GuardrailRuleNames))
		for i, n := range args.GuardrailRuleNames {
			existing.GuardrailRules[i] = models.GuardrailRuleAttribute{OrgID: orgID, AttributeName: args.Name, GuardrailRuleName: n}
		}
	} else {
		existing.GuardrailRules = nil
	}
	if args.DatamaskingRuleNames != nil {
		existing.DatamaskingRules = make([]models.DatamaskingRuleAttribute, len(args.DatamaskingRuleNames))
		for i, n := range args.DatamaskingRuleNames {
			existing.DatamaskingRules[i] = models.DatamaskingRuleAttribute{OrgID: orgID, AttributeName: args.Name, DatamaskingRuleName: n}
		}
	} else {
		existing.DatamaskingRules = nil
	}
	if args.AccessControlGroupNames != nil {
		existing.AccessControlGroups = make([]models.AccessControlGroupAttribute, len(args.AccessControlGroupNames))
		for i, n := range args.AccessControlGroupNames {
			existing.AccessControlGroups[i] = models.AccessControlGroupAttribute{OrgID: orgID, AttributeName: args.Name, GroupName: n}
		}
	} else {
		existing.AccessControlGroups = nil
	}

	if err := models.UpsertAttribute(models.DB, existing); err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return errResult("attribute not found"), nil, nil
		}
		return nil, nil, fmt.Errorf("failed updating attribute: %w", err)
	}

	// Re-fetch so the response carries the resolved associations even when callers
	// passed nil (preserve) for some fields.
	refreshed, err := models.GetAttribute(models.DB, orgID, args.Name)
	if err != nil {
		return nil, nil, fmt.Errorf("failed re-fetching attribute: %w", err)
	}
	return jsonResult(attributeToMap(refreshed))
}

func attributesDeleteHandler(ctx context.Context, _ *mcp.CallToolRequest, args attributesDeleteInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}
	if !sc.IsAdminUser() {
		return errResult("admin access required"), nil, nil
	}
	if args.Name == "" {
		return errResult("name is required"), nil, nil
	}

	orgID, err := uuid.Parse(sc.GetOrgID())
	if err != nil {
		return nil, nil, fmt.Errorf("invalid org id: %w", err)
	}

	switch err := models.DeleteAttribute(models.DB, orgID, args.Name); err {
	case nil:
		return textResult(fmt.Sprintf("attribute %q deleted successfully", args.Name)), nil, nil
	case models.ErrNotFound:
		return errResult("attribute not found"), nil, nil
	default:
		return nil, nil, fmt.Errorf("failed deleting attribute: %w", err)
	}
}

func buildAttributeFromInput(orgID uuid.UUID, name string, description *string,
	connectionNames, accessRequestRuleNames, guardrailRuleNames, datamaskingRuleNames, accessControlGroupNames []string) *models.Attribute {

	attr := &models.Attribute{
		OrgID:       orgID,
		Name:        name,
		Description: description,
	}
	if connectionNames != nil {
		attr.Connections = make([]models.ConnectionAttribute, len(connectionNames))
		for i, n := range connectionNames {
			attr.Connections[i] = models.ConnectionAttribute{OrgID: orgID, AttributeName: name, ConnectionName: n}
		}
	}
	if accessRequestRuleNames != nil {
		attr.AccessRequestRules = make([]models.AccessRequestRuleAttribute, len(accessRequestRuleNames))
		for i, n := range accessRequestRuleNames {
			attr.AccessRequestRules[i] = models.AccessRequestRuleAttribute{OrgID: orgID, AttributeName: name, AccessRuleName: n}
		}
	}
	if guardrailRuleNames != nil {
		attr.GuardrailRules = make([]models.GuardrailRuleAttribute, len(guardrailRuleNames))
		for i, n := range guardrailRuleNames {
			attr.GuardrailRules[i] = models.GuardrailRuleAttribute{OrgID: orgID, AttributeName: name, GuardrailRuleName: n}
		}
	}
	if datamaskingRuleNames != nil {
		attr.DatamaskingRules = make([]models.DatamaskingRuleAttribute, len(datamaskingRuleNames))
		for i, n := range datamaskingRuleNames {
			attr.DatamaskingRules[i] = models.DatamaskingRuleAttribute{OrgID: orgID, AttributeName: name, DatamaskingRuleName: n}
		}
	}
	if accessControlGroupNames != nil {
		attr.AccessControlGroups = make([]models.AccessControlGroupAttribute, len(accessControlGroupNames))
		for i, n := range accessControlGroupNames {
			attr.AccessControlGroups[i] = models.AccessControlGroupAttribute{OrgID: orgID, AttributeName: name, GroupName: n}
		}
	}
	return attr
}

func attributeToMap(a *models.Attribute) map[string]any {
	m := map[string]any{
		"id":         a.ID.String(),
		"name":       a.Name,
		"created_at": a.CreatedAt,
	}
	if a.Description != nil {
		m["description"] = *a.Description
	}
	if len(a.Connections) > 0 {
		names := make([]string, len(a.Connections))
		for i, ca := range a.Connections {
			names[i] = ca.ConnectionName
		}
		m["connection_names"] = names
	}
	if len(a.AccessRequestRules) > 0 {
		names := make([]string, len(a.AccessRequestRules))
		for i, arr := range a.AccessRequestRules {
			names[i] = arr.AccessRuleName
		}
		m["access_request_rule_names"] = names
	}
	if len(a.GuardrailRules) > 0 {
		names := make([]string, len(a.GuardrailRules))
		for i, gr := range a.GuardrailRules {
			names[i] = gr.GuardrailRuleName
		}
		m["guardrail_rule_names"] = names
	}
	if len(a.DatamaskingRules) > 0 {
		names := make([]string, len(a.DatamaskingRules))
		for i, dm := range a.DatamaskingRules {
			names[i] = dm.DatamaskingRuleName
		}
		m["datamasking_rule_names"] = names
	}
	if len(a.AccessControlGroups) > 0 {
		names := make([]string, len(a.AccessControlGroups))
		for i, acg := range a.AccessControlGroups {
			names[i] = acg.GroupName
		}
		m["access_control_group_names"] = names
	}
	return m
}
