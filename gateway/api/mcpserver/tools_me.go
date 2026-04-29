package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type meGetInput struct{}

func registerMeTools(server *mcp.Server) {
	openWorld := false

	mcp.AddTool(server, &mcp.Tool{
		Name: "me_get",
		Description: "Get the authenticated user's profile: email, name, user_id, org_id, " +
			"the groups they belong to, and whether they are an admin or auditor. " +
			"Useful as a first call to confirm which identity the MCP session is acting as.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, meGetHandler)
}

func meGetHandler(ctx context.Context, _ *mcp.CallToolRequest, _ meGetInput) (*mcp.CallToolResult, any, error) {
	sc := storageContextFrom(ctx)
	if sc == nil {
		return nil, nil, fmt.Errorf("unauthorized: missing auth context")
	}

	groups := sc.GetUserGroups()
	if groups == nil {
		groups = []string{}
	}
	return jsonResult(map[string]any{
		"user_id":     sc.UserID,
		"user_email":  sc.UserEmail,
		"user_name":   sc.UserName,
		"org_id":      sc.GetOrgID(),
		"org_name":    sc.OrgName,
		"groups":      groups,
		"is_admin":    sc.IsAdminUser(),
		"is_auditor":  sc.IsAuditorUser(),
		"user_status": sc.UserStatus,
	})
}
