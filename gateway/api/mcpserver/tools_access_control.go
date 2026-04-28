package mcpserver

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/lib/pq"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type accessControlSetInput struct {
	ConnectionName string   `json:"connection_name" jsonschema:"connection name to restrict"`
	Groups         []string `json:"groups" jsonschema:"user groups allowed to access this connection (replaces any existing list)"`
}

type accessControlUnsetInput struct {
	ConnectionName string `json:"connection_name" jsonschema:"connection name to remove the access_control binding from"`
}

type accessControlGetInput struct {
	ConnectionName string `json:"connection_name" jsonschema:"connection name"`
}

type accessControlListInput struct{}

func registerAccessControlTools(server *mcp.Server) {
	openWorld := false

	mcp.AddTool(server, &mcp.Tool{
		Name: "access_control_set",
		Description: "Restrict a connection so only the given user groups can access it. " +
			"Enables the access_control plugin if needed and replaces any existing group list " +
			"for this connection. Admins and auditors always bypass the restriction.",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: &openWorld},
	}, accessControlSetHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name: "access_control_unset",
		Description: "Remove the access_control binding for a connection. After this call, " +
			"the connection is no longer group-restricted (all users can see/access it).",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: &openWorld},
	}, accessControlUnsetHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "access_control_get",
		Description: "Get the list of user groups allowed to access a given connection via the access_control plugin",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, accessControlGetHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "access_control_list",
		Description: "List all connections currently bound to the access_control plugin with their allowed user groups",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, accessControlListHandler)
}

// ensureAccessControlPlugin returns the access_control plugin for the org,
// creating it on first use. It never deletes existing plugin_connections.
func ensureAccessControlPlugin(orgID string) (*models.Plugin, error) {
	p, err := models.GetPluginByName(orgID, plugintypes.PluginAccessControlName)
	if err == nil {
		return p, nil
	}
	if err != models.ErrNotFound {
		return nil, err
	}
	newPlugin := &models.Plugin{
		ID:    uuid.NewString(),
		OrgID: orgID,
		Name:  plugintypes.PluginAccessControlName,
	}
	if err := models.UpsertPlugin(newPlugin); err != nil {
		return nil, fmt.Errorf("failed enabling access_control plugin: %w", err)
	}
	return newPlugin, nil
}

func accessControlSetHandler(ctx context.Context, _ *mcp.CallToolRequest, args accessControlSetInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}
	if !sc.IsAdminUser() {
		return errResult("admin access required"), nil, nil
	}
	if args.ConnectionName == "" {
		return errResult("connection_name is required"), nil, nil
	}
	if len(args.Groups) == 0 {
		return errResult("groups must contain at least one user group; use access_control_unset to remove the binding"), nil, nil
	}

	conn, err := models.GetConnectionByName(models.DB, args.ConnectionName)
	if err != nil {
		return errResult(fmt.Sprintf("connection %q not found", args.ConnectionName)), nil, nil
	}

	if _, err := ensureAccessControlPlugin(sc.GetOrgID()); err != nil {
		return nil, nil, err
	}

	pc, err := models.UpsertPluginConnection(sc.GetOrgID(), plugintypes.PluginAccessControlName, conn.ID, pq.StringArray(args.Groups))
	if err != nil {
		return nil, nil, fmt.Errorf("failed setting access_control for connection %q: %w", args.ConnectionName, err)
	}

	return jsonResult(map[string]any{
		"connection_name": args.ConnectionName,
		"connection_id":   conn.ID,
		"allowed_groups":  []string(pc.Config),
	})
}

func accessControlUnsetHandler(ctx context.Context, _ *mcp.CallToolRequest, args accessControlUnsetInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}
	if !sc.IsAdminUser() {
		return errResult("admin access required"), nil, nil
	}
	if args.ConnectionName == "" {
		return errResult("connection_name is required"), nil, nil
	}

	conn, err := models.GetConnectionByName(models.DB, args.ConnectionName)
	if err != nil {
		return errResult(fmt.Sprintf("connection %q not found", args.ConnectionName)), nil, nil
	}

	err = models.DeletePluginConnection(sc.GetOrgID(), plugintypes.PluginAccessControlName, conn.ID)
	switch err {
	case nil:
		return textResult(fmt.Sprintf("access_control binding removed from connection %q", args.ConnectionName)), nil, nil
	case models.ErrNotFound:
		return errResult(fmt.Sprintf("connection %q has no access_control binding", args.ConnectionName)), nil, nil
	default:
		return nil, nil, fmt.Errorf("failed removing access_control binding: %w", err)
	}
}

func accessControlGetHandler(ctx context.Context, _ *mcp.CallToolRequest, args accessControlGetInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}
	if args.ConnectionName == "" {
		return errResult("connection_name is required"), nil, nil
	}

	conn, err := models.GetConnectionByName(models.DB, args.ConnectionName)
	if err != nil {
		return errResult(fmt.Sprintf("connection %q not found", args.ConnectionName)), nil, nil
	}

	pc, err := models.GetPluginConnection(sc.GetOrgID(), plugintypes.PluginAccessControlName, conn.ID)
	switch err {
	case nil:
		return jsonResult(map[string]any{
			"connection_name": args.ConnectionName,
			"connection_id":   conn.ID,
			"allowed_groups":  []string(pc.Config),
		})
	case models.ErrNotFound:
		return jsonResult(map[string]any{
			"connection_name": args.ConnectionName,
			"connection_id":   conn.ID,
			"allowed_groups":  []string{},
			"note":            "no access_control binding — connection is visible to all users",
		})
	default:
		return nil, nil, fmt.Errorf("failed retrieving access_control binding: %w", err)
	}
}

func accessControlListHandler(ctx context.Context, _ *mcp.CallToolRequest, _ accessControlListInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	plugin, err := models.GetPluginByName(sc.GetOrgID(), plugintypes.PluginAccessControlName)
	switch err {
	case nil:
	case models.ErrNotFound:
		return jsonResult([]any{})
	default:
		return nil, nil, fmt.Errorf("failed retrieving access_control plugin: %w", err)
	}

	out := make([]map[string]any, 0, len(plugin.Connections))
	for _, pc := range plugin.Connections {
		out = append(out, map[string]any{
			"connection_id":   pc.ConnectionID,
			"connection_name": pc.ConnectionName,
			"allowed_groups":  []string(pc.Config),
		})
	}
	return jsonResult(out)
}
