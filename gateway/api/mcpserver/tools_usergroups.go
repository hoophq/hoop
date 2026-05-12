package mcpserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/lib/pq"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gorm.io/gorm"
)

type usergroupsListInput struct{}

type usergroupsCreateInput struct {
	Name        string   `json:"name" jsonschema:"name of the user group to create"`
	Connections []string `json:"connections,omitempty" jsonschema:"optional connection names to grant the new group access to via the access_control plugin. Each connection's existing access_control bindings are preserved (the new group is appended)."`
	Attributes  []string `json:"attributes,omitempty" jsonschema:"optional existing attribute names to associate the new group with. Attributes must already exist."`
}

type usergroupsDeleteInput struct {
	Name string `json:"name" jsonschema:"name of the user group to delete"`
}

func registerUserGroupTools(server *mcp.Server) {
	openWorld := false

	mcp.AddTool(server, &mcp.Tool{
		Name:        "usergroups_list",
		Description: "List all user groups in the organization",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, usergroupsListHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name: "usergroups_create",
		Description: "Create a user group, optionally binding it to connections and/or existing attributes in the same call. " +
			"For 'create group X with access to connection Y' requests, pass `connections` to grant access via the access_control plugin (matches the webapp 'Create new access control group' form). " +
			"For approval / just-in-time workflows on a connection, use access_request_rules_create instead. " +
			"To bind an EXISTING group to connections, use access_control_set.",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: &openWorld},
	}, usergroupsCreateHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "usergroups_delete",
		Description: "Delete a user group by name. Cannot delete the admin group. This action is irreversible",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: &openWorld},
	}, usergroupsDeleteHandler)
}

func usergroupsListHandler(ctx context.Context, _ *mcp.CallToolRequest, _ usergroupsListInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	groups, err := models.GetUserGroupsByOrgID(sc.GetOrgID())
	if err != nil {
		return nil, nil, fmt.Errorf("failed listing user groups: %w", err)
	}

	// Deduplicate group names
	seen := make(map[string]bool)
	names := make([]string, 0)
	for _, g := range groups {
		if !seen[g.Name] {
			seen[g.Name] = true
			names = append(names, g.Name)
		}
	}

	return jsonResult(names)
}

func usergroupsCreateHandler(ctx context.Context, _ *mcp.CallToolRequest, args usergroupsCreateInput) (*mcp.CallToolResult, any, error) {
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

	orgID := sc.GetOrgID()
	parsedOrgID, err := uuid.Parse(orgID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid org id: %w", err)
	}

	if err := models.CreateUserGroupWithoutUser(orgID, args.Name); err != nil {
		return nil, nil, fmt.Errorf("failed creating user group: %w", err)
	}

	boundConnections := []map[string]any{}
	if len(args.Connections) > 0 {
		if _, err := ensureAccessControlPlugin(orgID); err != nil {
			return nil, nil, err
		}
		for _, connName := range args.Connections {
			conn, err := models.GetConnectionByName(models.DB, connName)
			if err != nil {
				return errResult(fmt.Sprintf("connection %q not found", connName)), nil, nil
			}

			existing := []string{}
			pc, err := models.GetPluginConnection(orgID, plugintypes.PluginAccessControlName, conn.ID)
			switch err {
			case nil:
				existing = []string(pc.Config)
			case models.ErrNotFound:
				// no existing binding, start fresh
			default:
				return nil, nil, fmt.Errorf("failed reading existing access_control binding for %q: %w", connName, err)
			}

			newConfig := appendIfMissing(existing, args.Name)
			updated, err := models.UpsertPluginConnection(orgID, plugintypes.PluginAccessControlName, conn.ID, pq.StringArray(newConfig))
			if err != nil {
				return nil, nil, fmt.Errorf("failed binding %q to access_control: %w", connName, err)
			}
			boundConnections = append(boundConnections, map[string]any{
				"connection_name": connName,
				"connection_id":   conn.ID,
				"allowed_groups":  []string(updated.Config),
			})
		}
	}

	boundAttributes := []string{}
	for _, attrName := range args.Attributes {
		attr, err := models.GetAttribute(models.DB, parsedOrgID, attrName)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errResult(fmt.Sprintf("attribute %q not found — create the attribute first", attrName)), nil, nil
			}
			return nil, nil, fmt.Errorf("failed fetching attribute %q: %w", attrName, err)
		}

		alreadyBound := false
		for _, g := range attr.AccessControlGroups {
			if g.GroupName == args.Name {
				alreadyBound = true
				break
			}
		}
		if !alreadyBound {
			attr.AccessControlGroups = append(attr.AccessControlGroups, models.AccessControlGroupAttribute{
				OrgID:         parsedOrgID,
				AttributeName: attrName,
				GroupName:     args.Name,
			})
			if err := models.UpsertAttribute(models.DB, attr); err != nil {
				return nil, nil, fmt.Errorf("failed updating attribute %q: %w", attrName, err)
			}
		}
		boundAttributes = append(boundAttributes, attrName)
	}

	return jsonResult(map[string]any{
		"group":             args.Name,
		"bound_connections": boundConnections,
		"bound_attributes":  boundAttributes,
	})
}

func appendIfMissing(s []string, val string) []string {
	for _, x := range s {
		if x == val {
			return s
		}
	}
	return append(s, val)
}

func usergroupsDeleteHandler(ctx context.Context, _ *mcp.CallToolRequest, args usergroupsDeleteInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}
	if !sc.IsAdminUser() {
		return errResult("admin access required"), nil, nil
	}

	if args.Name == types.GroupAdmin {
		return errResult("cannot delete the admin group"), nil, nil
	}

	err := models.DeleteUserGroup(sc.GetOrgID(), args.Name)
	if err != nil {
		return nil, nil, fmt.Errorf("failed deleting user group: %w", err)
	}

	return textResult(fmt.Sprintf("user group %q deleted successfully", args.Name)), nil, nil
}
