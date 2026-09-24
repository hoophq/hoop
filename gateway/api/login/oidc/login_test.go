package loginoidcapi

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/hoophq/hoop/gateway/appconfig"
	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// A control plane whose groups are provisioned must keep them on login
// (ADR-0019). appconfig.Load is one shot, so this binary runs as one.
func TestMain(m *testing.M) {
	os.Setenv("API_URL", "http://localhost:8009")
	if err := appconfig.Load(appconfig.AppModeControlPlane); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func TestSyncSingleTenantUserKeepsProvisionedGroups(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("start embedded database: %v", err)
	}
	t.Cleanup(func() { inst.Close(ctx) })
	if err := modelsbootstrap.MigrateDB(inst.MigrateDSN(), ""); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}
	if err := models.InitDatabaseConnection(inst.DSN(), 1); err != nil {
		t.Fatalf("open gorm connection: %v", err)
	}
	const orgID = "00000000-0000-0000-0000-0000000000e1"
	if err := models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'default')`, orgID).Error; err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if err := models.DB.Exec(`INSERT INTO private.users (org_id, subject, email, name, status)
		VALUES (?, 'idp|ana', 'ana@example.com', 'Ana', 'active')`, orgID).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	login := func() []string {
		t.Helper()
		userCtx, err := models.GetUserContext("idp|ana")
		if err != nil {
			t.Fatalf("user context: %v", err)
		}
		uinfo := idptypes.ProviderUserInfo{
			Subject: "idp|ana", Email: "ana@example.com",
			Groups: []string{"claim-group"}, MustSyncGroups: true,
		}
		if _, err := syncSingleTenantUser(userCtx, uinfo); err != nil {
			t.Fatalf("sync: %v", err)
		}
		after, err := models.GetUserContext("idp|ana")
		if err != nil {
			t.Fatalf("user context: %v", err)
		}
		groups := slices.Clone(after.UserGroups)
		slices.Sort(groups)
		return groups
	}

	// Without provisioning the claim decides, as it always did.
	if got := login(); !slices.Equal(got, []string{"claim-group"}) {
		t.Fatalf("groups = %v; want the claim's", got)
	}

	// With a SCIM token the provisioned groups stay.
	if err := models.DB.Exec(`DELETE FROM private.user_groups`).Error; err != nil {
		t.Fatal(err)
	}
	if err := models.DB.Exec(`INSERT INTO private.user_groups (org_id, user_id, name)
		SELECT org_id, id, 'dba-leads' FROM private.users WHERE subject = 'idp|ana'`).Error; err != nil {
		t.Fatalf("seed provisioned group: %v", err)
	}
	if err := models.ReplaceSCIMToken(models.DB, orgID, "hash", "admin@example.com"); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	if got := login(); !slices.Equal(got, []string{"dba-leads"}) {
		t.Fatalf("groups = %v; want the provisioned group kept", got)
	}
}
