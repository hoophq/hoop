package userapi

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestParseImportCSV(t *testing.T) {
	csv := "\uFEFFName,EMAIL,groups\n" +
		"Ana,ana@example.com,dba-leads; sre\n" +
		"\"Lima, Bob\",bob@example.com,\n" +
		",carl@example.com\n"
	rows, err := parseImportCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %+v; want 3", rows)
	}
	if rows[0].Email != "ana@example.com" || !slices.Equal(rows[0].Groups, []string{"dba-leads", "sre"}) {
		t.Errorf("row 1 = %+v", rows[0])
	}
	if rows[1].Name != "Lima, Bob" || len(rows[1].Groups) != 0 {
		t.Errorf("row 2 = %+v", rows[1])
	}
	if rows[2].Email != "carl@example.com" || rows[2].Name != "" {
		t.Errorf("row 3 = %+v; a short row reads as empty fields", rows[2])
	}

	if _, err := parseImportCSV(strings.NewReader("name,groups\nAna,sre\n")); err == nil {
		t.Errorf("a header without email must be refused")
	}
}

func startUserDB(t *testing.T, orgID string) {
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
	if err := models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'users-test')`, orgID).Error; err != nil {
		t.Fatalf("seed org: %v", err)
	}
}

// While a source manages the groups, a PUT may flip the admin group and
// nothing else.
func TestManagedGroupsUpdate(t *testing.T) {
	const orgID = "00000000-0000-0000-0000-0000000000f1"
	startUserDB(t, orgID)

	anaID := uuid.NewString()
	if err := models.DB.Exec(`INSERT INTO private.users (id, org_id, subject, email, name, status)
		VALUES (?, ?, 'idp|ana', 'ana@example.com', 'Ana', 'active')`, anaID, orgID).Error; err != nil {
		t.Fatalf("seed ana: %v", err)
	}
	if err := models.InsertUserGroups([]models.UserGroup{{OrgID: orgID, UserID: anaID, Name: "dba-leads"}}); err != nil {
		t.Fatalf("seed group: %v", err)
	}

	// Unmanaged: the request's groups go through as they are.
	got, status, _, err := managedGroupsUpdate(orgID, anaID, []string{"sre"})
	if err != nil || status != 0 || !slices.Equal(got, []string{"sre"}) {
		t.Fatalf("unmanaged = %v %d %v; want the request's groups", got, status, err)
	}

	if _, err := services.UpsertProvisionedUser(models.DB, orgID, models.ProvisioningSourceSlack, anaID,
		services.ProvisionedUser{UserName: "ana@example.com", Active: true}); err != nil {
		t.Fatalf("provision ana: %v", err)
	}

	got, status, _, err = managedGroupsUpdate(orgID, anaID, []string{"dba-leads", types.GroupAdmin})
	if err != nil || status != 0 || !slices.Equal(got, []string{"dba-leads", types.GroupAdmin}) {
		t.Errorf("promote = %v %d %v; want the admin added", got, status, err)
	}
	got, status, _, err = managedGroupsUpdate(orgID, anaID, []string{"dba-leads"})
	if err != nil || status != 0 || !slices.Equal(got, []string{"dba-leads"}) {
		t.Errorf("same groups = %v %d %v", got, status, err)
	}
	_, status, msg, err := managedGroupsUpdate(orgID, anaID, []string{"dba-leads", "sre"})
	if err != nil || status != http.StatusUnprocessableEntity || !strings.Contains(msg, "managed") {
		t.Errorf("changed groups = %d %q %v; want 422", status, msg, err)
	}
}
