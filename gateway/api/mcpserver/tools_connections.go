package mcpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type connectionsListInput struct {
	AgentID string   `json:"agent_id,omitempty" jsonschema:"filter by agent UUID"`
	Type    string   `json:"type,omitempty" jsonschema:"filter by connection type (database, application, custom)"`
	SubType string   `json:"subtype,omitempty" jsonschema:"filter by subtype (postgres, mysql, mongodb, mssql, oracledb, tcp, ssh, httpproxy)"`
	Tags    []string `json:"tags,omitempty" jsonschema:"filter by tags"`
	Name    string   `json:"name,omitempty" jsonschema:"filter by name pattern"`
	Search  string   `json:"search,omitempty" jsonschema:"search by name, type, subtype, resource name or status"`
}

type connectionsGetInput struct {
	Name string `json:"name" jsonschema:"the connection name or ID"`
}

type connectionsCreateInput struct {
	Name                    string            `json:"name" jsonschema:"unique connection name"`
	Type                    string            `json:"type" jsonschema:"connection type: database, application, or custom"`
	SubType                 string            `json:"subtype,omitempty" jsonschema:"connection subtype (postgres, mysql, mongodb, mssql, oracledb, tcp, ssh, httpproxy)"`
	AgentID                 string            `json:"agent_id,omitempty" jsonschema:"agent UUID to associate with"`
	Command                 []string          `json:"command,omitempty" jsonschema:"shell command to execute"`
	Secrets                 map[string]string `json:"secrets,omitempty" jsonschema:"environment variables (base64-encoded values)"`
	ConnectionTags          map[string]string `json:"tags,omitempty" jsonschema:"key-value tags"`
	AccessModeRunbooks      string            `json:"access_mode_runbooks,omitempty" jsonschema:"access mode for runbooks (enabled or disabled)"`
	AccessModeExec          string            `json:"access_mode_exec,omitempty" jsonschema:"access mode for exec (enabled or disabled)"`
	AccessModeConnect       string            `json:"access_mode_connect,omitempty" jsonschema:"access mode for connect (enabled or disabled)"`
	GuardRailRules          []string          `json:"guardrail_rules,omitempty" jsonschema:"list of guardrail rule IDs to apply"`
	Reviewers               []string          `json:"reviewers,omitempty" jsonschema:"list of reviewer group names"`
	RedactTypes             []string          `json:"redact_types,omitempty" jsonschema:"list of data types to redact"`
	MandatoryMetadataFields []string          `json:"mandatory_metadata_fields,omitempty" jsonschema:"required metadata fields for sessions"`
}

type connectionsUpdateInput struct {
	Name                    string            `json:"name" jsonschema:"connection name or ID to update"`
	AgentID                 *string           `json:"agent_id,omitempty" jsonschema:"agent UUID to associate with"`
	Command                 []string          `json:"command,omitempty" jsonschema:"shell command to execute"`
	Secrets                 map[string]string `json:"secrets,omitempty" jsonschema:"environment variables (base64-encoded values)"`
	ConnectionTags          map[string]string `json:"tags,omitempty" jsonschema:"key-value tags"`
	AccessModeRunbooks      string            `json:"access_mode_runbooks,omitempty" jsonschema:"access mode for runbooks (enabled or disabled)"`
	AccessModeExec          string            `json:"access_mode_exec,omitempty" jsonschema:"access mode for exec (enabled or disabled)"`
	AccessModeConnect       string            `json:"access_mode_connect,omitempty" jsonschema:"access mode for connect (enabled or disabled)"`
	GuardRailRules          []string          `json:"guardrail_rules,omitempty" jsonschema:"list of guardrail rule IDs to apply"`
	Reviewers               []string          `json:"reviewers,omitempty" jsonschema:"list of reviewer group names"`
	RedactTypes             []string          `json:"redact_types,omitempty" jsonschema:"list of data types to redact"`
	MandatoryMetadataFields []string          `json:"mandatory_metadata_fields,omitempty" jsonschema:"required metadata fields for sessions"`
}

type connectionsDeleteInput struct {
	Name string `json:"name" jsonschema:"connection name to delete"`
}

func boolPtr(b bool) *bool { return &b }

func registerConnectionTools(server *mcp.Server) {
	openWorld := false

	mcp.AddTool(server, &mcp.Tool{
		Name:        "connections_list",
		Description: "List all connections accessible to the authenticated user, with optional filters by agent, type, subtype, tags, or search query",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, connectionsListHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "connections_get",
		Description: "Get a single connection by its name or ID",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, connectionsGetHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "connections_create",
		Description: "Create a new connection. Requires admin access. Specify connection type, agent, command, secrets, and access modes",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: &openWorld},
	}, connectionsCreateHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "connections_update",
		Description: "Update an existing connection by name. Requires admin access. Only provided fields are updated",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: &openWorld},
	}, connectionsUpdateHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "connections_delete",
		Description: "Delete a connection by name. Requires admin access. This action is irreversible",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: &openWorld},
	}, connectionsDeleteHandler)
}

func connectionsListHandler(ctx context.Context, _ *mcp.CallToolRequest, args connectionsListInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	filterOpts := models.ConnectionFilterOption{
		AgentID: args.AgentID,
		Type:    args.Type,
		SubType: args.SubType,
		Tags:    args.Tags,
		Name:    args.Name,
		Search:  args.Search,
	}

	connections, err := models.ListConnections(sc, filterOpts)
	if err != nil {
		return nil, nil, fmt.Errorf("failed listing connections: %w", err)
	}

	// Clear secrets from response for security
	result := make([]map[string]any, 0, len(connections))
	for _, conn := range connections {
		result = append(result, connectionToMap(&conn, false))
	}

	return jsonResult(result)
}

func connectionsGetHandler(ctx context.Context, _ *mcp.CallToolRequest, args connectionsGetInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	conn, err := models.GetConnectionByNameOrID(sc, args.Name)
	if err != nil {
		return nil, nil, fmt.Errorf("failed fetching connection: %w", err)
	}
	if conn == nil {
		return errResult("connection not found"), nil, nil
	}

	return jsonResult(connectionToMap(conn, true))
}

func connectionsCreateHandler(ctx context.Context, _ *mcp.CallToolRequest, args connectionsCreateInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	existingConn, err := models.GetConnectionByNameOrID(sc, args.Name)
	if err != nil {
		return nil, nil, fmt.Errorf("failed checking existing connection: %w", err)
	}
	if existingConn != nil {
		return errResult("connection already exists"), nil, nil
	}

	conn := &models.Connection{
		ID:                      uuid.NewString(),
		OrgID:                   sc.GetOrgID(),
		AgentID:                 sql.NullString{String: args.AgentID, Valid: args.AgentID != ""},
		Name:                    args.Name,
		Command:                 args.Command,
		Type:                    args.Type,
		SubType:                 sql.NullString{String: args.SubType, Valid: args.SubType != ""},
		Envs:                    args.Secrets,
		Status:                  models.ConnectionStatusOffline,
		ConnectionTags:          args.ConnectionTags,
		AccessModeRunbooks:      args.AccessModeRunbooks,
		AccessModeExec:          args.AccessModeExec,
		AccessModeConnect:       args.AccessModeConnect,
		GuardRailRules:          args.GuardRailRules,
		Reviewers:               args.Reviewers,
		RedactTypes:             args.RedactTypes,
		MandatoryMetadataFields: args.MandatoryMetadataFields,
	}

	resp, err := models.UpsertConnection(sc, conn)
	if err != nil {
		return nil, nil, fmt.Errorf("failed creating connection: %w", err)
	}

	return jsonResult(connectionToMap(resp, false))
}

func connectionsUpdateHandler(ctx context.Context, _ *mcp.CallToolRequest, args connectionsUpdateInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	existing, err := models.GetConnectionByNameOrID(sc, args.Name)
	if err != nil {
		return nil, nil, fmt.Errorf("failed fetching connection: %w", err)
	}
	if existing == nil {
		return errResult("connection not found"), nil, nil
	}

	if existing.ManagedBy.Valid {
		return errResult(fmt.Sprintf("connection is managed by %q and cannot be updated via MCP", existing.ManagedBy.String)), nil, nil
	}

	// Apply updates to existing connection
	if args.AgentID != nil {
		existing.AgentID = sql.NullString{String: *args.AgentID, Valid: *args.AgentID != ""}
	}
	if args.Command != nil {
		existing.Command = args.Command
	}
	if args.Secrets != nil {
		existing.Envs = args.Secrets
	}
	if args.ConnectionTags != nil {
		existing.ConnectionTags = args.ConnectionTags
	}
	if args.AccessModeRunbooks != "" {
		existing.AccessModeRunbooks = args.AccessModeRunbooks
	}
	if args.AccessModeExec != "" {
		existing.AccessModeExec = args.AccessModeExec
	}
	if args.AccessModeConnect != "" {
		existing.AccessModeConnect = args.AccessModeConnect
	}
	if args.GuardRailRules != nil {
		existing.GuardRailRules = args.GuardRailRules
	}
	if args.Reviewers != nil {
		existing.Reviewers = args.Reviewers
	}
	if args.RedactTypes != nil {
		existing.RedactTypes = args.RedactTypes
	}
	if args.MandatoryMetadataFields != nil {
		existing.MandatoryMetadataFields = args.MandatoryMetadataFields
	}

	resp, err := models.UpsertConnection(sc, existing)
	if err != nil {
		return nil, nil, fmt.Errorf("failed updating connection: %w", err)
	}

	return jsonResult(connectionToMap(resp, false))
}

func connectionsDeleteHandler(ctx context.Context, _ *mcp.CallToolRequest, args connectionsDeleteInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	err := models.DeleteConnection(sc.GetOrgID(), args.Name)
	switch err {
	case models.ErrNotFound:
		return errResult("connection not found"), nil, nil
	case nil:
		return textResult(fmt.Sprintf("connection %q deleted successfully", args.Name)), nil, nil
	default:
		return nil, nil, fmt.Errorf("failed deleting connection: %w", err)
	}
}

func connectionToMap(conn *models.Connection, includeSecrets bool) map[string]any {
	m := map[string]any{
		"id":       conn.ID,
		"name":     conn.Name,
		"type":     conn.Type,
		"subtype":  conn.SubType.String,
		"agent_id": conn.AgentID.String,
		"status":   conn.Status,
		"command":  conn.Command,
	}
	if includeSecrets && len(conn.Envs) > 0 {
		m["secrets"] = conn.Envs
	}
	if len(conn.ConnectionTags) > 0 {
		m["tags"] = conn.ConnectionTags
	}
	if conn.AccessModeRunbooks != "" {
		m["access_mode_runbooks"] = conn.AccessModeRunbooks
	}
	if conn.AccessModeExec != "" {
		m["access_mode_exec"] = conn.AccessModeExec
	}
	if conn.AccessModeConnect != "" {
		m["access_mode_connect"] = conn.AccessModeConnect
	}
	if len(conn.GuardRailRules) > 0 {
		m["guardrail_rules"] = conn.GuardRailRules
	}
	if len(conn.Reviewers) > 0 {
		m["reviewers"] = conn.Reviewers
	}
	if conn.RedactEnabled {
		m["redact_enabled"] = true
	}
	if len(conn.RedactTypes) > 0 {
		m["redact_types"] = conn.RedactTypes
	}
	if conn.ManagedBy.Valid {
		m["managed_by"] = conn.ManagedBy.String
	}
	if len(conn.Attributes) > 0 {
		m["attributes"] = conn.Attributes
	}
	return m
}

// jsonResult marshals v to JSON and returns it as TextContent.
func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, nil, fmt.Errorf("failed marshaling result: %w", err)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(data)}},
	}, nil, nil
}

// textResult returns a plain text MCP result.
func textResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}

// errResult returns a plain text MCP error result.
func errResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		IsError: true,
	}
}
