package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type datamaskingListInput struct{}

type datamaskingGetInput struct {
	ID string `json:"id" jsonschema:"data masking rule ID"`
}

type supportedEntityTypesEntry struct {
	Name        string   `json:"name" jsonschema:"entity type group name (e.g. presidio)"`
	EntityTypes []string `json:"entity_types" jsonschema:"list of entity type identifiers"`
}

type customEntityTypesEntry struct {
	Name     string   `json:"name" jsonschema:"custom entity type name"`
	Regex    string   `json:"regex,omitempty" jsonschema:"regex pattern for detection"`
	DenyList []string `json:"deny_list,omitempty" jsonschema:"list of denied values"`
	Score    float64  `json:"score,omitempty" jsonschema:"confidence score threshold"`
}

type datamaskingCreateInput struct {
	Name                 string                      `json:"name" jsonschema:"unique rule name"`
	Description          string                      `json:"description,omitempty" jsonschema:"human-readable description"`
	ConnectionIDs        []string                    `json:"connection_ids,omitempty" jsonschema:"connection IDs to apply this rule to"`
	SupportedEntityTypes []supportedEntityTypesEntry `json:"supported_entity_types,omitempty" jsonschema:"built-in entity types to detect"`
	CustomEntityTypes    []customEntityTypesEntry    `json:"custom_entity_types,omitempty" jsonschema:"custom entity type definitions"`
	ScoreThreshold       *float64                    `json:"score_threshold,omitempty" jsonschema:"minimum confidence score for detection"`
}

type datamaskingUpdateInput struct {
	ID                   string                      `json:"id" jsonschema:"data masking rule ID to update"`
	Description          string                      `json:"description,omitempty" jsonschema:"human-readable description"`
	ConnectionIDs        []string                    `json:"connection_ids,omitempty" jsonschema:"connection IDs to apply this rule to"`
	SupportedEntityTypes []supportedEntityTypesEntry `json:"supported_entity_types,omitempty" jsonschema:"built-in entity types to detect"`
	CustomEntityTypes    []customEntityTypesEntry    `json:"custom_entity_types,omitempty" jsonschema:"custom entity type definitions"`
	ScoreThreshold       *float64                    `json:"score_threshold,omitempty" jsonschema:"minimum confidence score for detection"`
}

type datamaskingDeleteInput struct {
	ID string `json:"id" jsonschema:"data masking rule ID to delete"`
}

func registerDataMaskingTools(server *mcp.Server) {
	openWorld := false

	mcp.AddTool(server, &mcp.Tool{
		Name:        "datamasking_rules_list",
		Description: "List all data masking rules configured for the organization",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, datamaskingListHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "datamasking_rules_get",
		Description: "Get a single data masking rule by its ID",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, datamaskingGetHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "datamasking_rules_create",
		Description: "Create a new data masking rule with entity type detection configuration",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: &openWorld},
	}, datamaskingCreateHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "datamasking_rules_update",
		Description: "Update an existing data masking rule by ID",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: &openWorld},
	}, datamaskingUpdateHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "datamasking_rules_delete",
		Description: "Delete a data masking rule by ID. This action is irreversible",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: &openWorld},
	}, datamaskingDeleteHandler)
}

func datamaskingListHandler(ctx context.Context, _ *mcp.CallToolRequest, _ datamaskingListInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	rules, err := models.ListDataMaskingRules(sc.GetOrgID())
	if err != nil {
		return nil, nil, fmt.Errorf("failed listing data masking rules: %w", err)
	}

	result := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		result = append(result, datamaskingToMap(&rule))
	}
	return jsonResult(result)
}

func datamaskingGetHandler(ctx context.Context, _ *mcp.CallToolRequest, args datamaskingGetInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	rule, err := models.GetDataMaskingRuleByID(sc.GetOrgID(), args.ID)
	if err == models.ErrNotFound {
		return errResult("data masking rule not found"), nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed fetching data masking rule: %w", err)
	}

	return jsonResult(datamaskingToMap(rule))
}

func datamaskingCreateHandler(ctx context.Context, _ *mcp.CallToolRequest, args datamaskingCreateInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	rule := &models.DataMaskingRule{
		ID:                   uuid.NewString(),
		OrgID:                sc.GetOrgID(),
		Name:                 args.Name,
		Description:          args.Description,
		SupportedEntityTypes: toModelSupportedEntityTypes(args.SupportedEntityTypes),
		CustomEntityTypes:    toModelCustomEntityTypes(args.CustomEntityTypes),
		ScoreThreshold:       args.ScoreThreshold,
		ConnectionIDs:        filterEmptyStrings(args.ConnectionIDs),
		UpdatedAt:            time.Now().UTC(),
	}

	resp, err := models.CreateDataMaskingRule(rule)
	if err == models.ErrAlreadyExists {
		return errResult("data masking rule already exists"), nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed creating data masking rule: %w", err)
	}

	return jsonResult(datamaskingToMap(resp))
}

func datamaskingUpdateHandler(ctx context.Context, _ *mcp.CallToolRequest, args datamaskingUpdateInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	rule := &models.DataMaskingRule{
		ID:                   args.ID,
		OrgID:                sc.GetOrgID(),
		Description:          args.Description,
		SupportedEntityTypes: toModelSupportedEntityTypes(args.SupportedEntityTypes),
		CustomEntityTypes:    toModelCustomEntityTypes(args.CustomEntityTypes),
		ScoreThreshold:       args.ScoreThreshold,
		ConnectionIDs:        filterEmptyStrings(args.ConnectionIDs),
		UpdatedAt:            time.Now().UTC(),
	}

	resp, err := models.UpdateDataMaskingRule(rule)
	if err == models.ErrNotFound {
		return errResult("data masking rule not found"), nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed updating data masking rule: %w", err)
	}

	return jsonResult(datamaskingToMap(resp))
}

func datamaskingDeleteHandler(ctx context.Context, _ *mcp.CallToolRequest, args datamaskingDeleteInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	err := models.DeleteDataMaskingRule(sc.GetOrgID(), args.ID)
	switch err {
	case models.ErrNotFound:
		return errResult("data masking rule not found"), nil, nil
	case nil:
		return textResult(fmt.Sprintf("data masking rule %q deleted successfully", args.ID)), nil, nil
	default:
		return nil, nil, fmt.Errorf("failed deleting data masking rule: %w", err)
	}
}

func datamaskingToMap(rule *models.DataMaskingRule) map[string]any {
	m := map[string]any{
		"id":          rule.ID,
		"name":        rule.Name,
		"description": rule.Description,
		"updated_at":  rule.UpdatedAt,
	}
	if len(rule.SupportedEntityTypes) > 0 {
		m["supported_entity_types"] = rule.SupportedEntityTypes
	}
	if len(rule.CustomEntityTypes) > 0 {
		m["custom_entity_types"] = rule.CustomEntityTypes
	}
	if rule.ScoreThreshold != nil {
		m["score_threshold"] = *rule.ScoreThreshold
	}
	if len(rule.ConnectionIDs) > 0 {
		m["connection_ids"] = rule.ConnectionIDs
	}
	if len(rule.Attributes) > 0 {
		m["attributes"] = rule.Attributes
	}
	return m
}

func toModelSupportedEntityTypes(entries []supportedEntityTypesEntry) models.SupportedEntityTypesList {
	if len(entries) == 0 {
		return nil
	}
	result := make(models.SupportedEntityTypesList, len(entries))
	for i, e := range entries {
		result[i] = models.SupportedEntityTypesEntry{
			Name:        e.Name,
			EntityTypes: e.EntityTypes,
		}
	}
	return result
}

func toModelCustomEntityTypes(entries []customEntityTypesEntry) models.CustomEntityTypesList {
	if len(entries) == 0 {
		return nil
	}
	result := make(models.CustomEntityTypesList, len(entries))
	for i, e := range entries {
		result[i] = models.CustomEntityTypesEntry{
			Name:     e.Name,
			Regex:    e.Regex,
			DenyList: e.DenyList,
			Score:    e.Score,
		}
	}
	return result
}
