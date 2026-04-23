package mcpserver

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/license"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gorm.io/gorm"
)

type accessRequestRulesListInput struct {
	Page     int `json:"page,omitempty" jsonschema:"page number (default 1)"`
	PageSize int `json:"page_size,omitempty" jsonschema:"page size (default 0 for all)"`
}

type accessRequestRulesGetInput struct {
	Name string `json:"name" jsonschema:"access request rule name"`
}

type accessRequestRulesCreateInput struct {
	Name                   string   `json:"name" jsonschema:"unique rule name"`
	Description            *string  `json:"description,omitempty" jsonschema:"human-readable description"`
	AccessType             string   `json:"access_type" jsonschema:"access type: jit or command"`
	ConnectionNames        []string `json:"connection_names,omitempty" jsonschema:"target connection names"`
	ApprovalRequiredGroups []string `json:"approval_required_groups" jsonschema:"groups that must approve"`
	ReviewersGroups        []string `json:"reviewers_groups" jsonschema:"groups that can review"`
	ForceApprovalGroups    []string `json:"force_approval_groups,omitempty" jsonschema:"groups that can force approve"`
	AllGroupsMustApprove   bool     `json:"all_groups_must_approve" jsonschema:"whether all groups must approve"`
	MinApprovals           *int     `json:"min_approvals,omitempty" jsonschema:"minimum number of approvals required"`
	AccessMaxDuration      *int     `json:"access_max_duration,omitempty" jsonschema:"maximum access duration in seconds"`
	Attributes             []string `json:"attributes,omitempty" jsonschema:"target attributes instead of connections"`
}

type accessRequestRulesUpdateInput struct {
	Name                   string   `json:"name" jsonschema:"access request rule name to update"`
	Description            *string  `json:"description,omitempty" jsonschema:"human-readable description"`
	AccessType             string   `json:"access_type" jsonschema:"access type: jit or command"`
	ConnectionNames        []string `json:"connection_names,omitempty" jsonschema:"target connection names"`
	ApprovalRequiredGroups []string `json:"approval_required_groups" jsonschema:"groups that must approve"`
	ReviewersGroups        []string `json:"reviewers_groups" jsonschema:"groups that can review"`
	ForceApprovalGroups    []string `json:"force_approval_groups,omitempty" jsonschema:"groups that can force approve"`
	AllGroupsMustApprove   bool     `json:"all_groups_must_approve" jsonschema:"whether all groups must approve"`
	MinApprovals           *int     `json:"min_approvals,omitempty" jsonschema:"minimum number of approvals required"`
	AccessMaxDuration      *int     `json:"access_max_duration,omitempty" jsonschema:"maximum access duration in seconds"`
	Attributes             []string `json:"attributes,omitempty" jsonschema:"target attributes instead of connections"`
}

type accessRequestRulesDeleteInput struct {
	Name string `json:"name" jsonschema:"access request rule name to delete"`
}

func registerAccessRequestRuleTools(server *mcp.Server) {
	openWorld := false

	mcp.AddTool(server, &mcp.Tool{
		Name:        "access_request_rules_list",
		Description: "List all access request rules for the organization with optional pagination",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, accessRequestRulesListHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "access_request_rules_get",
		Description: "Get a single access request rule by name",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, accessRequestRulesGetHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "access_request_rules_create",
		Description: "Create a new access request rule. Requires admin access. Defines approval workflows for connections",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: &openWorld},
	}, accessRequestRulesCreateHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "access_request_rules_update",
		Description: "Update an existing access request rule by name. Requires admin access",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: &openWorld},
	}, accessRequestRulesUpdateHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "access_request_rules_delete",
		Description: "Delete an access request rule by name. Requires admin access. This action is irreversible",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: &openWorld},
	}, accessRequestRulesDeleteHandler)
}

func accessRequestRulesListHandler(ctx context.Context, _ *mcp.CallToolRequest, args accessRequestRulesListInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}
	if !sc.IsAuditorOrAdminUser() {
		return errResult("admin or auditor access required"), nil, nil
	}

	orgID, err := uuid.Parse(sc.GetOrgID())
	if err != nil {
		return nil, nil, fmt.Errorf("invalid organization ID: %w", err)
	}

	opts := models.AccessRequestRulesFilterOption{
		Page:     args.Page,
		PageSize: args.PageSize,
	}
	if opts.Page <= 0 {
		opts.Page = 1
	}

	rules, total, err := models.ListAccessRequestRules(models.DB, orgID, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("failed listing access request rules: %w", err)
	}

	items := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		items = append(items, accessRequestRuleToMap(&rule))
	}

	result := map[string]any{
		"pages": map[string]any{
			"total": total,
			"page":  opts.Page,
			"size":  opts.PageSize,
		},
		"data": items,
	}
	return jsonResult(result)
}

func accessRequestRulesGetHandler(ctx context.Context, _ *mcp.CallToolRequest, args accessRequestRulesGetInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}
	if !sc.IsAuditorOrAdminUser() {
		return errResult("admin or auditor access required"), nil, nil
	}

	orgID, err := uuid.Parse(sc.GetOrgID())
	if err != nil {
		return nil, nil, fmt.Errorf("invalid organization ID: %w", err)
	}

	rule, err := models.GetAccessRequestRuleByName(models.DB, args.Name, orgID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return errResult("access request rule not found"), nil, nil
		}
		return nil, nil, fmt.Errorf("failed fetching access request rule: %w", err)
	}

	return jsonResult(accessRequestRuleToMap(rule))
}

func accessRequestRulesCreateHandler(ctx context.Context, _ *mcp.CallToolRequest, args accessRequestRulesCreateInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}
	if !sc.IsAdminUser() {
		return errResult("admin access required"), nil, nil
	}

	orgID, err := uuid.Parse(sc.GetOrgID())
	if err != nil {
		return nil, nil, fmt.Errorf("invalid organization ID: %w", err)
	}

	// Validate input
	if err := validateAccessRequestRuleInput(&args); err != nil {
		return errResult(err.Error()), nil, nil
	}

	// OSS license check: max 1 rule
	if sc.GetLicenseType() == license.OSSType {
		count, err := models.CountAccessRequestRules(models.DB, orgID)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to count access request rules: %w", err)
		}
		if count >= 1 {
			return errResult("access request rules are limited to 1 in the OSS version"), nil, nil
		}
	}

	// Duplicate check
	foundRule, err := models.GetAccessRequestRuleByResourceNamesAndAccessType(models.DB, orgID, args.ConnectionNames, args.AccessType)
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, nil, fmt.Errorf("failed to check existing access request rule: %w", err)
	}
	if foundRule != nil {
		return errResult("an access request rule with the same connection names and access type already exists"), nil, nil
	}

	if len(args.Attributes) > 0 {
		found, err := models.GetRequestRulesByAttributes(models.DB, orgID, args.Attributes, args.AccessType)
		if err != nil && err != gorm.ErrRecordNotFound {
			return nil, nil, fmt.Errorf("failed to check existing attributes rule: %w", err)
		}
		if found != nil {
			return errResult("an access request rule with the same attributes and access type already exists"), nil, nil
		}
	}

	rule := &models.AccessRequestRule{
		OrgID:                  orgID,
		Name:                   args.Name,
		Description:            args.Description,
		AccessType:             args.AccessType,
		ConnectionNames:        ensureStringArray(args.ConnectionNames),
		ApprovalRequiredGroups: ensureStringArray(args.ApprovalRequiredGroups),
		AllGroupsMustApprove:   args.AllGroupsMustApprove,
		ReviewersGroups:        ensureStringArray(args.ReviewersGroups),
		ForceApprovalGroups:    ensureStringArray(args.ForceApprovalGroups),
		AccessMaxDuration:      args.AccessMaxDuration,
		MinApprovals:           args.MinApprovals,
	}

	if err := models.CreateAccessRequestRule(models.DB, rule); err != nil {
		if err == gorm.ErrDuplicatedKey {
			return errResult("access request rule with the same name already exists"), nil, nil
		}
		return nil, nil, fmt.Errorf("failed creating access request rule: %w", err)
	}

	// Handle attributes
	if len(args.Attributes) > 0 {
		if err := models.UpsertAccessRequestRuleAttributes(models.DB, orgID, rule.Name, args.Attributes); err != nil {
			return nil, nil, fmt.Errorf("failed to upsert access request rule attributes: %w", err)
		}
		for _, attr := range args.Attributes {
			rule.RuleAttributes = append(rule.RuleAttributes, models.AccessRequestRuleAttribute{
				OrgID: orgID, AttributeName: attr, AccessRuleName: rule.Name,
			})
		}
	}

	return jsonResult(accessRequestRuleToMap(rule))
}

func accessRequestRulesUpdateHandler(ctx context.Context, _ *mcp.CallToolRequest, args accessRequestRulesUpdateInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}
	if !sc.IsAdminUser() {
		return errResult("admin access required"), nil, nil
	}

	orgID, err := uuid.Parse(sc.GetOrgID())
	if err != nil {
		return nil, nil, fmt.Errorf("invalid organization ID: %w", err)
	}

	// Validate input
	if err := validateAccessRequestRuleInputForUpdate(&args); err != nil {
		return errResult(err.Error()), nil, nil
	}

	existingRule, err := models.GetAccessRequestRuleByName(models.DB, args.Name, orgID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return errResult("access request rule not found"), nil, nil
		}
		return nil, nil, fmt.Errorf("failed fetching access request rule: %w", err)
	}

	// Duplicate check for connection names + access type
	foundRule, err := models.GetAccessRequestRuleByResourceNamesAndAccessType(models.DB, orgID, args.ConnectionNames, args.AccessType)
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, nil, fmt.Errorf("failed to check existing access request rule: %w", err)
	}
	if foundRule != nil && foundRule.ID != existingRule.ID {
		return errResult("an access request rule with the same connection names and access type already exists"), nil, nil
	}

	if len(args.Attributes) > 0 {
		found, err := models.GetRequestRulesByAttributes(models.DB, orgID, args.Attributes, args.AccessType)
		if err != nil && err != gorm.ErrRecordNotFound {
			return nil, nil, fmt.Errorf("failed to check existing attributes rule: %w", err)
		}
		if found != nil && found.ID != existingRule.ID {
			return errResult("an access request rule with the same attributes and access type already exists"), nil, nil
		}
	}

	// Update fields
	existingRule.Name = args.Name
	existingRule.Description = args.Description
	existingRule.AccessType = args.AccessType
	existingRule.ConnectionNames = ensureStringArray(args.ConnectionNames)
	existingRule.ApprovalRequiredGroups = ensureStringArray(args.ApprovalRequiredGroups)
	existingRule.AllGroupsMustApprove = args.AllGroupsMustApprove
	existingRule.ReviewersGroups = ensureStringArray(args.ReviewersGroups)
	existingRule.ForceApprovalGroups = ensureStringArray(args.ForceApprovalGroups)
	existingRule.AccessMaxDuration = args.AccessMaxDuration
	existingRule.MinApprovals = args.MinApprovals

	if err := models.UpdateAccessRequestRule(models.DB, existingRule); err != nil {
		return nil, nil, fmt.Errorf("failed updating access request rule: %w", err)
	}

	// Handle attributes
	if err := models.UpsertAccessRequestRuleAttributes(models.DB, orgID, existingRule.Name, args.Attributes); err != nil {
		return nil, nil, fmt.Errorf("failed to upsert access request rule attributes: %w", err)
	}
	existingRule.RuleAttributes = make([]models.AccessRequestRuleAttribute, 0, len(args.Attributes))
	for _, attr := range args.Attributes {
		existingRule.RuleAttributes = append(existingRule.RuleAttributes, models.AccessRequestRuleAttribute{
			OrgID: orgID, AttributeName: attr, AccessRuleName: existingRule.Name,
		})
	}

	return jsonResult(accessRequestRuleToMap(existingRule))
}

func accessRequestRulesDeleteHandler(ctx context.Context, _ *mcp.CallToolRequest, args accessRequestRulesDeleteInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}
	if !sc.IsAdminUser() {
		return errResult("admin access required"), nil, nil
	}

	orgID, err := uuid.Parse(sc.GetOrgID())
	if err != nil {
		return nil, nil, fmt.Errorf("invalid organization ID: %w", err)
	}

	err = models.DeleteAccessRequestRuleByName(models.DB, args.Name, orgID)
	switch err {
	case gorm.ErrRecordNotFound:
		return errResult("access request rule not found"), nil, nil
	case nil:
		return textResult(fmt.Sprintf("access request rule %q deleted successfully", args.Name)), nil, nil
	default:
		return nil, nil, fmt.Errorf("failed deleting access request rule: %w", err)
	}
}

func validateAccessRequestRuleInput(args *accessRequestRulesCreateInput) error {
	if err := apivalidation.ValidateResourceName(args.Name); err != nil {
		return err
	}
	if len(args.ConnectionNames) == 0 && len(args.Attributes) == 0 {
		return fmt.Errorf("either connection_names or attributes must have at least 1 entry")
	}
	if args.AccessType != "jit" && args.AccessType != "command" {
		return fmt.Errorf("access_type must be either 'jit' or 'command'")
	}
	if len(args.ApprovalRequiredGroups) == 0 {
		return fmt.Errorf("approval_required_groups must have at least 1 entry")
	}
	if len(args.ReviewersGroups) == 0 {
		return fmt.Errorf("reviewers_groups must have at least 1 entry")
	}
	if !args.AllGroupsMustApprove && (args.MinApprovals == nil || *args.MinApprovals < 1) {
		return fmt.Errorf("min_approvals must be at least 1 when all_groups_must_approve is false")
	}
	return nil
}

func validateAccessRequestRuleInputForUpdate(args *accessRequestRulesUpdateInput) error {
	if len(args.ConnectionNames) == 0 && len(args.Attributes) == 0 {
		return fmt.Errorf("either connection_names or attributes must have at least 1 entry")
	}
	if args.AccessType != "jit" && args.AccessType != "command" {
		return fmt.Errorf("access_type must be either 'jit' or 'command'")
	}
	if len(args.ApprovalRequiredGroups) == 0 {
		return fmt.Errorf("approval_required_groups must have at least 1 entry")
	}
	if len(args.ReviewersGroups) == 0 {
		return fmt.Errorf("reviewers_groups must have at least 1 entry")
	}
	if !args.AllGroupsMustApprove && (args.MinApprovals == nil || *args.MinApprovals < 1) {
		return fmt.Errorf("min_approvals must be at least 1 when all_groups_must_approve is false")
	}
	return nil
}

// ensureStringArray returns an empty array instead of nil to satisfy NOT NULL DB constraints.
func ensureStringArray(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func accessRequestRuleToMap(rule *models.AccessRequestRule) map[string]any {
	m := map[string]any{
		"id":                       rule.ID.String(),
		"name":                     rule.Name,
		"access_type":              rule.AccessType,
		"all_groups_must_approve":  rule.AllGroupsMustApprove,
		"created_at":               rule.CreatedAt,
		"updated_at":               rule.UpdatedAt,
	}
	if rule.Description != nil {
		m["description"] = *rule.Description
	}
	if len(rule.ConnectionNames) > 0 {
		m["connection_names"] = []string(rule.ConnectionNames)
	}
	if len(rule.ApprovalRequiredGroups) > 0 {
		m["approval_required_groups"] = []string(rule.ApprovalRequiredGroups)
	}
	if len(rule.ReviewersGroups) > 0 {
		m["reviewers_groups"] = []string(rule.ReviewersGroups)
	}
	if len(rule.ForceApprovalGroups) > 0 {
		m["force_approval_groups"] = []string(rule.ForceApprovalGroups)
	}
	if rule.MinApprovals != nil {
		m["min_approvals"] = *rule.MinApprovals
	}
	if rule.AccessMaxDuration != nil {
		m["access_max_duration"] = *rule.AccessMaxDuration
	}
	if len(rule.RuleAttributes) > 0 {
		attrs := make([]map[string]string, 0, len(rule.RuleAttributes))
		for _, attr := range rule.RuleAttributes {
			attrs = append(attrs, map[string]string{
				"attribute_name": attr.AttributeName,
			})
		}
		m["rule_attributes"] = attrs
	}
	return m
}
