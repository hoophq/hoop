package mcpserver

import (
	"context"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// connections_create and connections_update refuse an agent of another org.
func TestConnectionToolsRefuseAgentOfAnotherOrg(t *testing.T) {
	sc := startMirrorsDB(t)
	const otherOrgID = "00000000-0000-0000-0000-0000000000d5"
	require.NoError(t, models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'other-org')`, otherOrgID).Error)
	require.NoError(t, models.CreateAgent(otherOrgID, "_default", "standard", "hash-other"))
	other, err := models.GetAgentByNameOrID(otherOrgID, "_default")
	require.NoError(t, err)
	ctx := withStorageContext(context.Background(), sc)

	for _, agentID := range []string{other.ID, "00000000-0000-0000-0000-0000000011ff"} {
		res, _, err := connectionsCreateHandler(ctx, nil, connectionsCreateInput{
			Name: "conn-x", Type: "custom", SubType: "custom", AgentID: agentID, Command: []string{"bash"}})
		require.NoError(t, err)
		require.True(t, res.IsError)
		assert.Equal(t, "agent not found", res.Content[0].(*mcp.TextContent).Text)

		res, _, err = connectionsUpdateHandler(ctx, nil, connectionsUpdateInput{Name: "plain", AgentID: &agentID})
		require.NoError(t, err)
		require.True(t, res.IsError)
		assert.Equal(t, "agent not found", res.Content[0].(*mcp.TextContent).Text)
	}

	var names []string
	require.NoError(t, models.DB.Raw(`SELECT name FROM private.connections WHERE org_id = ? AND agent_id IS NOT NULL`,
		mirrorsTestOrgID).Scan(&names).Error)
	assert.Empty(t, names)
}
