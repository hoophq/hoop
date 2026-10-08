package sidecarmirrorstatus

import (
	"context"
	"os"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	"github.com/stretchr/testify/require"
)

const testOrgID = "00000000-0000-0000-0000-0000000000c3"

func TestMain(m *testing.M) { os.Exit(pglitetest.Main(m)) }

func startDB(t *testing.T) {
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
	require.NoError(t, models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'sweep-test')`, testOrgID).Error)
}

func status(t *testing.T) string {
	t.Helper()
	var out string
	require.NoError(t, models.DB.Raw(`SELECT status::text FROM private.connections WHERE name = 'pay-appdb'`).Scan(&out).Error)
	return out
}

// One sweep keeps a mirror online while its sidecar checks in, and sets it
// offline once the sidecar is silent past SidecarMirrorOfflineAfter.
func TestASweepSetsASilentSidecarsMirrorOffline(t *testing.T) {
	startDB(t)
	ctx := context.Background()
	sc := &models.Sidecar{OrgID: testOrgID, Name: "pay", KeyHash: models.HashAPIKey("hsc_pay"), CreatedBy: "tests@hoop.dev"}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	require.NoError(t, models.DB.Exec(`INSERT INTO private.resources (org_id, name, type, subtype) VALUES (?, 'pay-appdb', 'database', 'postgres')`, testOrgID).Error)
	require.NoError(t, models.DB.Exec(`INSERT INTO private.connections (org_id, name, type, subtype, resource_name, status, managed_by, sidecar_id, sidecar_listener)
		VALUES (?, 'pay-appdb', 'database', 'postgres', 'pay-appdb', 'offline', 'sidecar', ?, 'appdb')`, testOrgID, sc.ID).Error)

	require.NoError(t, models.RecordSidecarHandshake(models.DB, sc.ID, "1.0.0", "", "", "", "", nil))
	require.NoError(t, models.MarkSidecarConnectionsOnline(models.DB, testOrgID, sc.ID))
	sweep(ctx, models.DB)
	require.Equal(t, models.ConnectionStatusOnline, status(t), "fresh check-in: online")

	require.NoError(t, models.DB.Exec(`UPDATE private.sidecars SET last_seen_at = NOW() - interval '181 seconds' WHERE id = ?`, sc.ID).Error)
	sweep(ctx, models.DB)
	require.Equal(t, models.ConnectionStatusOffline, status(t), "silent sidecar: offline")
}
