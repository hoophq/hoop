package mcpserver

import (
	"context"
	"fmt"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type usersListInput struct{}

type usersGetInput struct {
	Email string `json:"email" jsonschema:"user email address"`
}

func registerUserTools(server *mcp.Server) {
	openWorld := false

	mcp.AddTool(server, &mcp.Tool{
		Name:        "users_list",
		Description: "List all users in the organization",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, usersListHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "users_get",
		Description: "Get a single user by their email address",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, usersGetHandler)
}

func usersListHandler(ctx context.Context, _ *mcp.CallToolRequest, _ usersListInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	users, err := models.ListUsers(sc.GetOrgID())
	if err != nil {
		return nil, nil, fmt.Errorf("failed listing users: %w", err)
	}

	result := make([]map[string]any, 0, len(users))
	for _, user := range users {
		result = append(result, userToMap(&user))
	}
	return jsonResult(result)
}

func usersGetHandler(ctx context.Context, _ *mcp.CallToolRequest, args usersGetInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	user, err := models.GetUserByEmailAndOrg(args.Email, sc.GetOrgID())
	if err != nil {
		return nil, nil, fmt.Errorf("failed fetching user: %w", err)
	}
	if user == nil {
		return errResult("user not found"), nil, nil
	}

	return jsonResult(userToMap(user))
}

func userToMap(user *models.User) map[string]any {
	m := map[string]any{
		"id":       user.ID,
		"email":    user.Email,
		"name":     user.Name,
		"status":   user.Status,
		"verified": user.Verified,
	}
	if user.SlackID != "" {
		m["slack_id"] = user.SlackID
	}
	return m
}
