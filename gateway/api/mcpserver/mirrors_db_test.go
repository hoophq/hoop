package mcpserver

import (
	"context"
	"os"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

const mirrorsTestOrgID = "00000000-0000-0000-0000-0000000000d4"

func TestMain(m *testing.M) { os.Exit(pglitetest.Main(m)) }

// startMirrorsDB seeds one plain connection and one sidecar mirror.
func startMirrorsDB(t *testing.T) *storagev2.Context {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	// The embedded backend serves one session at a time.
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	sqlDB, err := models.DB.DB()
	require.NoError(t, err)
	// Cleanup runs in reverse order: close the pool before shutting down PGlite.
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close test database pool: %v", err)
		}
	})
	exec := func(q string, args ...any) { require.NoError(t, models.DB.Exec(q, args...).Error) }
	exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'mirrors-test')`, mirrorsTestOrgID)
	sc := &models.Sidecar{OrgID: mirrorsTestOrgID, Name: "pay", KeyHash: models.HashAPIKey("hsc_pay"), CreatedBy: "tests@hoop.dev"}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	exec(`INSERT INTO private.resources (org_id, name, type, subtype) VALUES (?, 'plain', 'database', 'postgres'), (?, 'pay-appdb', 'database', 'postgres')`,
		mirrorsTestOrgID, mirrorsTestOrgID)
	exec(`INSERT INTO private.connections (org_id, name, type, subtype, resource_name) VALUES (?, 'plain', 'database', 'postgres', 'plain')`, mirrorsTestOrgID)
	exec(`INSERT INTO private.connections (org_id, name, type, subtype, resource_name, managed_by, sidecar_id, sidecar_listener)
		VALUES (?, 'pay-appdb', 'database', 'postgres', 'pay-appdb', 'sidecar', ?, 'appdb')`, mirrorsTestOrgID, sc.ID)
	return storagev2.NewContext("user-1", mirrorsTestOrgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", []string{types.GroupAdmin})
}

// connections_list shows no sidecar mirror: the sidecar owns it.
func TestConnectionsListShowsNoSidecarMirror(t *testing.T) {
	sc := startMirrorsDB(t)
	res, _, err := connectionsListHandler(withStorageContext(context.Background(), sc), nil, connectionsListInput{})
	require.NoError(t, err)
	text := res.Content[0].(*mcp.TextContent).Text
	require.Contains(t, text, `"plain"`)
	require.NotContains(t, text, "pay-appdb")
}
