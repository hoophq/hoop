package mcpserver

import (
	"context"
	"fmt"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type usergroupsListInput struct{}

type usergroupsCreateInput struct {
	Name string `json:"name" jsonschema:"name of the user group to create"`
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
		Name:        "usergroups_create",
		Description: "Create a new user group in the organization",
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

	err := models.CreateUserGroupWithoutUser(sc.GetOrgID(), args.Name)
	if err != nil {
		return nil, nil, fmt.Errorf("failed creating user group: %w", err)
	}

	return textResult(fmt.Sprintf("user group %q created successfully", args.Name)), nil, nil
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
