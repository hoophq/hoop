package services

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

func userStatus(t *testing.T, email string) string {
	t.Helper()
	var status string
	if err := models.DB.Raw(`SELECT status FROM private.users WHERE org_id = ? AND email = ?`,
		provisioningOrgID, email).Scan(&status).Error; err != nil {
		t.Fatalf("status of %s: %v", email, err)
	}
	return status
}

func userIDByEmail(t *testing.T, email string) string {
	t.Helper()
	var id string
	if err := models.DB.Raw(`SELECT id::TEXT FROM private.users WHERE org_id = ? AND email = ?`,
		provisioningOrgID, email).Scan(&id).Error; err != nil || id == "" {
		t.Fatalf("id of %s: %q %v", email, id, err)
	}
	return id
}

func TestImportUsers(t *testing.T) {
	startProvisioningDB(t)
	db := models.DB

	// The org's admin, who is also in the file.
	rootID := uuid.NewString()
	if err := db.Exec(`INSERT INTO private.users (id, org_id, subject, email, name, status)
		VALUES (?, ?, 'idp|root', 'root@example.com', 'Root', 'active')`, rootID, provisioningOrgID).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if err := models.InsertUserGroups([]models.UserGroup{{OrgID: provisioningOrgID, UserID: rootID, Name: types.GroupAdmin}}); err != nil {
		t.Fatalf("seed admin group: %v", err)
	}

	res, err := ImportUsers(db, provisioningOrgID, []ImportRow{
		{Row: 1, Email: "Ana@Example.com", Name: "Ana", Groups: []string{"dba-leads", " sre "}},
		{Row: 2, Email: "bob@example.com", Groups: []string{"dba-leads"}},
		{Row: 3, Email: "root@example.com", Groups: []string{"sre"}},
		{Row: 4, Email: "not-an-email", Groups: []string{"dba-leads"}},
		{Row: 5, Email: "eve@example.com", Groups: []string{"admin"}},
	}, false)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Created != 2 || res.Updated != 1 {
		t.Errorf("created %d updated %d; want 2 and 1", res.Created, res.Updated)
	}
	if len(res.Errors) != 2 || res.Errors[0].Row != 4 || res.Errors[1].Row != 5 ||
		!strings.Contains(res.Errors[1].Message, "reserved") {
		t.Fatalf("errors = %+v; want rows 4 (bad email) and 5 (reserved group)", res.Errors)
	}
	if got := userGroupNames(t, userIDByEmail(t, "ana@example.com")); !slices.Equal(got, []string{"dba-leads", "sre"}) {
		t.Errorf("ana groups = %v", got)
	}
	if got := userGroupNames(t, rootID); !slices.Equal(got, []string{types.GroupAdmin, "sre"}) {
		t.Errorf("root groups = %v; the admin row must stay", got)
	}
	var eves int64
	db.Model(&models.User{}).Where("org_id = ? AND email = 'eve@example.com'", provisioningOrgID).Count(&eves)
	if eves != 0 {
		t.Errorf("the row naming the admin group created eve")
	}
	// A file import is the admin's own edit: it does not manage the groups.
	if managed, err := models.GroupsManagedByProvisioning(db, provisioningOrgID); err != nil || managed {
		t.Errorf("after a file import: managed=%v err=%v; want false", managed, err)
	}

	t.Run("a second file sets the members of the groups it names", func(t *testing.T) {
		res, err := ImportUsers(db, provisioningOrgID, []ImportRow{
			{Row: 1, Email: "bob@example.com", Groups: []string{"dba-leads"}},
			{Row: 2, Email: "carl@example.com", Groups: []string{"dba-leads"}},
		}, true)
		if err != nil {
			t.Fatalf("import: %v", err)
		}
		if res.Created != 1 || res.Updated != 1 || res.Deactivated != 1 {
			t.Errorf("result = %+v; want carl created, bob updated, ana deactivated", res)
		}
		if got := userStatus(t, "ana@example.com"); got != "inactive" {
			t.Errorf("ana = %s; deactivate_missing must deactivate her", got)
		}
		if got := userGroupNames(t, userIDByEmail(t, "ana@example.com")); len(got) != 0 {
			t.Errorf("ana groups = %v; a deactivated user loses the imported groups", got)
		}
		if got := userStatus(t, "root@example.com"); got != "active" {
			t.Errorf("root = %s; an admin is never deactivated", got)
		}
		members, _ := models.ListGroupMemberIDs(db, provisioningOrgID, "dba-leads")
		if len(members) != 2 {
			t.Errorf("dba-leads has %d members; want bob and carl", len(members))
		}
	})

	t.Run("a file import is refused while SCIM manages the groups", func(t *testing.T) {
		if _, err := UpsertProvisionedUser(db, provisioningOrgID, models.ProvisioningSourceSCIM, "",
			ProvisionedUser{ExternalID: "okta-dan", UserName: "dan@example.com", Active: true}); err != nil {
			t.Fatalf("provision dan: %v", err)
		}
		_, err := ImportUsers(db, provisioningOrgID, []ImportRow{{Row: 1, Email: "fay@example.com"}}, false)
		if !errors.Is(err, ErrGroupsManaged) {
			t.Fatalf("err = %v; want ErrGroupsManaged", err)
		}
	})
}
