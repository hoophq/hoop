package services

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const provisioningOrgID = "00000000-0000-0000-0000-0000000000b1"

// startProvisioningDB boots the embedded database with the real schema: the
// provisioning writes are raw SQL across users, user_groups and the rules, and
// a fake would not catch what breaks there.
func startProvisioningDB(t *testing.T) {
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
	if err := models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'provisioning-test')`,
		provisioningOrgID).Error; err != nil {
		t.Fatalf("seed org: %v", err)
	}
}

func userGroupNames(t *testing.T, userID string) []string {
	t.Helper()
	rows, err := models.GetUserGroupsByUserID(userID)
	if err != nil {
		t.Fatalf("list groups of %s: %v", userID, err)
	}
	var names []string
	for _, r := range rows {
		names = append(names, r.Name)
	}
	slices.Sort(names)
	return names
}

func TestProvisioning(t *testing.T) {
	startProvisioningDB(t)
	db := models.DB

	// A user who logged in before provisioning, with a manual admin group.
	existingID := uuid.NewString()
	if err := db.Exec(`INSERT INTO private.users (id, org_id, subject, email, name, status)
		VALUES (?, ?, 'idp|carla', 'Carla@Example.com', 'Carla', 'active')`,
		existingID, provisioningOrgID).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := models.InsertUserGroups([]models.UserGroup{{OrgID: provisioningOrgID, UserID: existingID, Name: "admin"}}); err != nil {
		t.Fatalf("seed admin group: %v", err)
	}

	managed, err := models.GroupsManagedByProvisioning(db, provisioningOrgID)
	if err != nil || managed {
		t.Fatalf("before any token: managed=%v err=%v; want false", managed, err)
	}

	t.Run("creates a user with a placeholder subject", func(t *testing.T) {
		id, err := UpsertProvisionedUser(db, provisioningOrgID, models.ProvisioningSourceSCIM, "",
			ProvisionedUser{ExternalID: "okta-ana", UserName: "ana@example.com", Name: "Ana", Active: true})
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
		users, err := models.ListApproverUsersByEmailAndOrg(db, provisioningOrgID, "ana@example.com")
		if err != nil || len(users) != 1 || users[0].ID != id {
			t.Fatalf("got %+v err %v; want one active user %s", users, err, id)
		}
		if users[0].Subject != "scim|okta-ana" || users[0].Name != "Ana" {
			t.Fatalf("got subject %q name %q", users[0].Subject, users[0].Name)
		}

		again, err := UpsertProvisionedUser(db, provisioningOrgID, models.ProvisioningSourceSCIM, "",
			ProvisionedUser{ExternalID: "okta-ana", UserName: "ana@example.com", Email: "ana.new@example.com", Active: true})
		if err != nil || again != id {
			t.Fatalf("second upsert by external id: got %s err %v; want %s", again, err, id)
		}
		users, _ = models.ListApproverUsersByEmailAndOrg(db, provisioningOrgID, "ana.new@example.com")
		if len(users) != 1 || users[0].Name != "Ana" {
			t.Fatalf("email change: got %+v; want the same user, name kept", users)
		}
	})

	t.Run("adopts the user who already logged in", func(t *testing.T) {
		id, err := UpsertProvisionedUser(db, provisioningOrgID, models.ProvisioningSourceSCIM, "",
			ProvisionedUser{ExternalID: "okta-carla", UserName: "carla@example.com", Active: true})
		if err != nil || id != existingID {
			t.Fatalf("got %s err %v; want the existing user %s", id, err, existingID)
		}
		var u models.User
		if err := db.Where("id = ?", existingID).First(&u).Error; err != nil || u.Subject != "idp|carla" {
			t.Fatalf("subject changed to %q (err %v); the login's subject must stay", u.Subject, err)
		}
		taken, err := ProvisionedUserNameTaken(db, provisioningOrgID, models.ProvisioningSourceSCIM, "CARLA@example.com")
		if err != nil || !taken {
			t.Fatalf("user name taken = %v, err %v; want true", taken, err)
		}
	})

	t.Run("refuses an ambiguous email", func(t *testing.T) {
		for _, s := range []string{"dup-1", "dup-2"} {
			if err := db.Exec(`INSERT INTO private.users (org_id, subject, email, name, status)
				VALUES (?, ?, 'dup@example.com', 'Dup', 'active')`, provisioningOrgID, s).Error; err != nil {
				t.Fatalf("seed dup: %v", err)
			}
		}
		_, err := UpsertProvisionedUser(db, provisioningOrgID, models.ProvisioningSourceSCIM, "",
			ProvisionedUser{UserName: "dup@example.com", Active: true})
		if !errors.Is(err, ErrAmbiguousEmail) {
			t.Fatalf("err = %v; want ErrAmbiguousEmail", err)
		}
	})

	t.Run("refuses a user with no email", func(t *testing.T) {
		_, err := UpsertProvisionedUser(db, provisioningOrgID, models.ProvisioningSourceSCIM, "",
			ProvisionedUser{UserName: "not-an-email", Active: true})
		if !errors.Is(err, ErrProvisionedUserEmailRequired) {
			t.Fatalf("err = %v; want ErrProvisionedUserEmailRequired", err)
		}
	})

	var dbaGroup *models.DirectoryGroup
	anaID := func() string {
		users, _ := models.ListApproverUsersByEmailAndOrg(db, provisioningOrgID, "ana.new@example.com")
		if len(users) != 1 {
			t.Fatalf("ana not found")
		}
		return users[0].ID
	}()

	t.Run("groups and members", func(t *testing.T) {
		var err error
		dbaGroup, err = CreateProvisionedGroup(db, provisioningOrgID, models.ProvisioningSourceSCIM, "dba-leads", "okta-g1")
		if err != nil {
			t.Fatalf("create group: %v", err)
		}
		if _, err := CreateProvisionedGroup(db, provisioningOrgID, models.ProvisioningSourceSCIM, "dba-leads", ""); !errors.Is(err, ErrProvisionedGroupExists) {
			t.Fatalf("duplicate group: err = %v; want ErrProvisionedGroupExists", err)
		}

		if err := SetGroupMembers(db, provisioningOrgID, "dba-leads", []string{anaID, existingID, uuid.NewString(), "garbage"}); err != nil {
			t.Fatalf("set members: %v", err)
		}
		members, _ := models.ListGroupMemberIDs(db, provisioningOrgID, "dba-leads")
		if len(members) != 2 {
			t.Fatalf("members = %v; want ana and carla, unknown ids skipped", members)
		}

		if err := RemoveGroupMembers(db, provisioningOrgID, "dba-leads", []string{existingID}); err != nil {
			t.Fatalf("remove member: %v", err)
		}
		if got := userGroupNames(t, existingID); !slices.Equal(got, []string{"admin"}) {
			t.Fatalf("carla groups = %v; want only admin", got)
		}
		if err := AddGroupMembers(db, provisioningOrgID, "dba-leads", []string{existingID}); err != nil {
			t.Fatalf("add member: %v", err)
		}
		if got := userGroupNames(t, existingID); !slices.Equal(got, []string{"admin", "dba-leads"}) {
			t.Fatalf("carla groups = %v; want admin and dba-leads", got)
		}

		all, err := models.GetUserGroupsByOrgID(provisioningOrgID)
		if err != nil {
			t.Fatalf("list org groups: %v", err)
		}
		var empty int
		for _, g := range all {
			if g.Name == "dba-leads" && g.UserID == "" {
				empty++
			}
		}
		if empty != 1 {
			t.Fatalf("member-less rows for dba-leads = %d; want 1", empty)
		}
	})

	t.Run("a rename reaches the approval rules", func(t *testing.T) {
		orgUUID := uuid.MustParse(provisioningOrgID)
		one := 1
		if err := models.CreateAccessRequestRule(db, &models.AccessRequestRule{
			OrgID: orgUUID, Name: "hold-deletes", AccessType: models.AccessTypeSidecar,
			ConnectionNames: []string{}, ApprovalRequiredGroups: []string{},
			ReviewersGroups: []string{"dba-leads", "admin"}, ForceApprovalGroups: []string{"dba-leads"},
			MinApprovals: &one,
		}); err != nil {
			t.Fatalf("create rule: %v", err)
		}

		if err := RenameProvisionedGroup(db, provisioningOrgID, dbaGroup, "database-leads"); err != nil {
			t.Fatalf("rename: %v", err)
		}
		if got := userGroupNames(t, anaID); !slices.Equal(got, []string{"database-leads"}) {
			t.Fatalf("ana groups = %v; want database-leads", got)
		}
		rule, err := models.GetAccessRequestRuleByName(db, "hold-deletes", orgUUID)
		if err != nil {
			t.Fatalf("load rule: %v", err)
		}
		if !slices.Equal([]string(rule.ReviewersGroups), []string{"database-leads", "admin"}) ||
			!slices.Equal([]string(rule.ForceApprovalGroups), []string{"database-leads"}) {
			t.Fatalf("rule groups = %v / %v; want the new name", rule.ReviewersGroups, rule.ForceApprovalGroups)
		}
	})

	t.Run("an inactive user loses provisioned groups only", func(t *testing.T) {
		if _, err := UpsertProvisionedUser(db, provisioningOrgID, models.ProvisioningSourceSCIM, existingID,
			ProvisionedUser{UserName: "carla@example.com", Active: false}); err != nil {
			t.Fatalf("deactivate: %v", err)
		}
		if got := userGroupNames(t, existingID); !slices.Equal(got, []string{"admin"}) {
			t.Fatalf("carla groups = %v; want only the manual admin group", got)
		}
		if users, _ := models.ListApproverUsersByEmailAndOrg(db, provisioningOrgID, "carla@example.com"); len(users) != 0 {
			t.Fatalf("carla is still active")
		}
	})

	t.Run("deleting a group removes its rows", func(t *testing.T) {
		if err := DeleteProvisionedGroup(db, provisioningOrgID, dbaGroup); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if got := userGroupNames(t, anaID); len(got) != 0 {
			t.Fatalf("ana groups = %v; want none", got)
		}
		if _, err := models.GetDirectoryGroup(db, provisioningOrgID, dbaGroup.ID); err == nil {
			t.Fatalf("directory group survived")
		}
	})

	if err := models.ReplaceSCIMToken(db, provisioningOrgID, "hash", "admin@example.com"); err != nil {
		t.Fatalf("store token: %v", err)
	}
	managed, err = models.GroupsManagedByProvisioning(db, provisioningOrgID)
	if err != nil || !managed {
		t.Fatalf("with a token: managed=%v err=%v; want true", managed, err)
	}
}
