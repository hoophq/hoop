package loginoidcapi

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/appconfig"
	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	_ "github.com/jackc/pgx/v5/stdlib"
	"gorm.io/gorm"
)

// A control plane whose groups are provisioned must keep them on login
// (ADR-0020). appconfig.Load is one shot, so this binary runs as one.
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

	// Once the Slack import owns a group, the imported groups stay. The
	// import stores the email lower case with a placeholder subject; the
	// login must find that user and keep one.
	johnID := uuid.NewString()
	err = models.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`INSERT INTO private.users (id, org_id, subject, email, name, status, slack_id)
			VALUES (?, ?, 'slack|U-JOHN', 'john.doe@corp.com', 'John', 'active', 'U-JOHN')`, johnID, orgID).Error; err != nil {
			return err
		}
		if err := tx.Create(&models.DirectoryGroup{OrgID: orgID, ExternalID: "S-DBA", DisplayName: "dba-leads"}).Error; err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO private.user_groups (org_id, user_id, name) VALUES (?, ?, 'dba-leads')`, orgID, johnID).Error
	})
	if err != nil {
		t.Fatalf("import john: %v", err)
	}

	loginAs := func(email, subject string) []string {
		t.Helper()
		dbUser, err := models.GetUserByEmail(email)
		if err != nil || dbUser == nil {
			t.Fatalf("login lookup of %s: user %v err %v", email, dbUser, err)
		}
		userCtx, err := models.GetUserContext(dbUser.Subject)
		if err != nil {
			t.Fatalf("user context: %v", err)
		}
		uinfo := idptypes.ProviderUserInfo{Subject: subject, Email: email,
			Groups: []string{"claim-group"}, MustSyncGroups: true}
		if _, err := syncSingleTenantUser(userCtx, uinfo); err != nil {
			t.Fatalf("sync: %v", err)
		}
		after, err := models.GetUserContext(subject)
		if err != nil {
			t.Fatalf("user context: %v", err)
		}
		groups := slices.Clone(after.UserGroups)
		slices.Sort(groups)
		return groups
	}
	if got := loginAs("john.doe@corp.com", "idp|john"); !slices.Equal(got, []string{"dba-leads"}) {
		t.Fatalf("john groups = %v; want the provisioned group kept", got)
	}
	var johns int64
	models.DB.Model(&models.User{}).Where("org_id = ? AND lower(email) = 'john.doe@corp.com'", orgID).Count(&johns)
	if johns != 1 {
		t.Fatalf("%d users with john's email; the login must adopt the provisioned one", johns)
	}

	// Ana was never provisioned, but the org's groups are managed now: her
	// claim no longer rewrites them either.
	if got := login(); !slices.Equal(got, []string{"claim-group"}) {
		t.Fatalf("ana groups = %v; want the groups she had", got)
	}

	// Releasing the groups hands them back to the claim.
	if err := models.ClearProvisioningLinks(models.DB, orgID); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := loginAs("john.doe@corp.com", "idp|john"); !slices.Equal(got, []string{"claim-group"}) {
		t.Fatalf("john groups = %v; want the claim's after release", got)
	}
}
