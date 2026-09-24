package directorysync

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const syncOrgID = "00000000-0000-0000-0000-0000000000c1"

func startSyncDB(t *testing.T) {
	t.Helper()
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
	if err := models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'sync-test')`, syncOrgID).Error; err != nil {
		t.Fatalf("seed org: %v", err)
	}
}

type fakeProvider struct {
	groups  []Group
	members map[string][]User
	err     error
}

func (f *fakeProvider) ListGroups(context.Context) ([]Group, error) { return f.groups, f.err }
func (f *fakeProvider) ListMembers(_ context.Context, id string) ([]User, error) {
	return f.members[id], f.err
}

func activeEmails(t *testing.T) []string {
	t.Helper()
	var emails []string
	if err := models.DB.Raw(`SELECT email FROM private.users WHERE org_id = ? AND status = 'active' ORDER BY email`,
		syncOrgID).Scan(&emails).Error; err != nil {
		t.Fatalf("list users: %v", err)
	}
	return emails
}

func groupsOf(t *testing.T, email string) []string {
	t.Helper()
	var names []string
	if err := models.DB.Raw(`
		SELECT ug.name FROM private.user_groups ug JOIN private.users u ON u.id = ug.user_id
		WHERE u.org_id = ? AND u.email = ? ORDER BY ug.name`, syncOrgID, email).Scan(&names).Error; err != nil {
		t.Fatalf("list groups: %v", err)
	}
	return names
}

func TestReconcile(t *testing.T) {
	startSyncDB(t)
	ctx := context.Background()
	src := models.ProvisioningSourceGoogle

	// An admin who logged in before the sync existed.
	adminID := uuid.NewString()
	if err := models.DB.Exec(`INSERT INTO private.users (id, org_id, subject, email, name, status)
		VALUES (?, ?, 'idp|root', 'root@example.com', 'Root', 'active')`, adminID, syncOrgID).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if err := models.InsertUserGroups([]models.UserGroup{{OrgID: syncOrgID, UserID: adminID, Name: types.GroupAdmin}}); err != nil {
		t.Fatalf("seed admin group: %v", err)
	}

	p := &fakeProvider{
		groups: []Group{{ID: "g1", Name: "dba@example.com"}, {ID: "g2", Name: "sre@example.com"}, {ID: "g3", Name: "other@example.com"}},
		members: map[string][]User{
			"g1": {
				{ExternalID: "u1", Email: "ana@example.com", Name: "Ana", Active: true},
				{ExternalID: "u2", Email: "bob@example.com", Name: "Bob", Active: false},
				{ExternalID: "root", Email: "root@example.com", Name: "Root", Active: true},
			},
			"g2": {{ExternalID: "u1", Email: "ana@example.com", Name: "Ana", Active: true}},
		},
	}

	if err := reconcile(ctx, models.DB, syncOrgID, src, p, []string{"g1", "g2"}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if got := activeEmails(t); !slices.Equal(got, []string{"ana@example.com", "root@example.com"}) {
		t.Fatalf("active users = %v; want ana and root, bob is suspended", got)
	}
	if got := groupsOf(t, "ana@example.com"); !slices.Equal(got, []string{"dba@example.com", "sre@example.com"}) {
		t.Fatalf("ana groups = %v", got)
	}
	if got := groupsOf(t, "bob@example.com"); len(got) != 0 {
		t.Fatalf("suspended bob holds %v", got)
	}

	// A provider error mid-run must not touch anyone.
	failing := &fakeProvider{groups: p.groups, err: errors.New("directory unavailable")}
	if err := reconcile(ctx, models.DB, syncOrgID, src, failing, []string{"g1"}); err == nil {
		t.Fatalf("want the provider error")
	}
	if got := activeEmails(t); !slices.Equal(got, []string{"ana@example.com", "root@example.com"}) {
		t.Fatalf("after a failed run, active users = %v; want unchanged", got)
	}

	// A selected group that vanished from the provider fails the run.
	if err := reconcile(ctx, models.DB, syncOrgID, src, p, []string{"g1", "missing"}); err == nil {
		t.Fatalf("want an error for a missing group")
	}

	// Narrow the selection to g2 and rename it upstream: ana stays, root (an
	// admin) keeps access but loses the synced group, g1 disappears.
	p.groups[1].Name = "site-reliability@example.com"
	if err := reconcile(ctx, models.DB, syncOrgID, src, p, []string{"g2"}); err != nil {
		t.Fatalf("narrowed run: %v", err)
	}
	if got := activeEmails(t); !slices.Equal(got, []string{"ana@example.com", "root@example.com"}) {
		t.Fatalf("active users = %v; the admin must stay active", got)
	}
	if got := groupsOf(t, "root@example.com"); !slices.Equal(got, []string{types.GroupAdmin}) {
		t.Fatalf("root groups = %v; want only admin", got)
	}
	if got := groupsOf(t, "ana@example.com"); !slices.Equal(got, []string{"site-reliability@example.com"}) {
		t.Fatalf("ana groups = %v; want the renamed group only", got)
	}
	if _, err := models.GetDirectoryGroupByName(models.DB, syncOrgID, "dba@example.com"); err == nil {
		t.Fatalf("the deselected group survived")
	}

	// Ana leaves g2: she is out of scope and deactivated.
	p.members["g2"] = nil
	if err := reconcile(ctx, models.DB, syncOrgID, src, p, []string{"g2"}); err != nil {
		t.Fatalf("empty run: %v", err)
	}
	if got := activeEmails(t); !slices.Equal(got, []string{"root@example.com"}) {
		t.Fatalf("active users = %v; want only root", got)
	}
}
