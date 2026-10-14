package models_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"gorm.io/gorm"
)

func seedUserGroups(t *testing.T, subject string, groups ...string) {
	t.Helper()
	seedUser(t, subject, 0)
	for _, group := range groups {
		execSQL(t, `
			INSERT INTO private.user_groups (org_id, user_id, name)
			SELECT org_id, id, ? FROM private.users WHERE org_id = ? AND subject = ?`,
			group, testOrgID, subject)
	}
}

// seedAttributeGroup links a group to a connection through an attribute.
func seedAttributeGroup(t *testing.T, attribute, connName, group string) {
	t.Helper()
	execSQL(t, `INSERT INTO private.attributes (org_id, name) VALUES (?, ?)`, testOrgID, attribute)
	execSQL(t, `INSERT INTO private.connections_attributes (org_id, connection_name, attribute_name)
		VALUES (?, ?, ?)`, testOrgID, connName, attribute)
	execSQL(t, `INSERT INTO private.access_control_groups_attributes (org_id, group_name, attribute_name)
		VALUES (?, ?, ?)`, testOrgID, group, attribute)
}

func listAllConnectionUsers(t *testing.T, connName string) map[string]models.ConnectionUser {
	t.Helper()
	items, total, err := models.ListConnectionUsers(models.DB, testOrgID, connName, 1, 100)
	if err != nil {
		t.Fatalf("list connection users: %v", err)
	}
	if int(total) != len(items) {
		t.Fatalf("total: want %d, got %d", len(items), total)
	}
	bySubject := map[string]models.ConnectionUser{}
	for _, item := range items {
		bySubject[item.Subject] = item
	}
	return bySubject
}

// assertMatchesConnectionRead checks, user by user, that the list holds exactly
// the users whose own context can read the connection.
func assertMatchesConnectionRead(t *testing.T, connName string, subjects []string, listed map[string]models.ConnectionUser) {
	t.Helper()
	for _, subject := range subjects {
		ctx, err := models.GetUserContext(subject)
		if err != nil {
			t.Fatalf("user context %s: %v", subject, err)
		}
		conn, err := models.GetConnectionByNameOrID(ctx, connName)
		if err != nil {
			t.Fatalf("read connection as %s: %v", subject, err)
		}
		_, isListed := listed[subject]
		if canRead := conn != nil; canRead != isListed {
			t.Errorf("%s: connection read allows=%v, list has user=%v", subject, canRead, isListed)
		}
	}
}

// One embedded database for every case: each boot costs tens of seconds, and
// the package shares a 15 minute budget. The cases run in order on purpose:
// the plugin-off case must run before the access_control plugin exists.
func TestListConnectionUsersMatchesConnectionRead(t *testing.T) {
	startTestDB(t)

	// Without the access_control plugin every user can open every connection.
	seedConnection(t, "open", "enabled")
	seedUserGroups(t, "u-eng", "eng")
	seedUserGroups(t, "u-none")
	listed := listAllConnectionUsers(t, "open")
	assertMatchesConnectionRead(t, "open", []string{"u-eng", "u-none"}, listed)
	for _, subject := range []string{"u-eng", "u-none"} {
		if got := listed[subject].AccessType; got != models.ConnectionAccessAll {
			t.Errorf("plugin off, %s access_type: want %q, got %q", subject, models.ConnectionAccessAll, got)
		}
	}

	if _, _, err := models.ListConnectionUsers(models.DB, testOrgID, "missing", 1, 10); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("unknown connection: want gorm.ErrRecordNotFound, got %v", err)
	}

	seedPluginConnection(t, "access_control", "target", true, []string{"sre"})
	seedAttributeGroup(t, "pii", "target", "dba")
	// Grants on another connection must not leak into the target.
	seedPluginConnection(t, "access_control", "other", true, []string{"eng"})
	seedAttributeGroup(t, "billing", "other", "finance")

	seedUserGroups(t, "u-admin", types.GroupAdmin)
	seedUserGroups(t, "u-auditor", types.GroupAuditor)
	seedUserGroups(t, "u-sre", "sre")
	seedUserGroups(t, "u-dba", "dba")
	seedUserGroups(t, "u-both", "sre", "dba", "eng")
	seedUserGroups(t, "u-finance", "finance")
	subjects := []string{"u-admin", "u-auditor", "u-sre", "u-dba", "u-both", "u-eng", "u-finance", "u-none"}

	listed = listAllConnectionUsers(t, "target")
	assertMatchesConnectionRead(t, "target", subjects, listed)

	want := map[string]struct {
		accessType      string
		groups          []string
		attributeGroups []string
	}{
		"u-admin":   {models.ConnectionAccessAdmin, nil, nil},
		"u-auditor": {models.ConnectionAccessAuditor, nil, nil},
		"u-sre":     {models.ConnectionAccessGroups, []string{"sre"}, nil},
		"u-dba":     {models.ConnectionAccessGroups, nil, []string{"dba"}},
		"u-both":    {models.ConnectionAccessGroups, []string{"sre"}, []string{"dba"}},
	}
	if len(listed) != len(want) {
		t.Fatalf("listed users: want %d, got %d (%v)", len(want), len(listed), listed)
	}
	for subject, w := range want {
		got, ok := listed[subject]
		if !ok {
			t.Errorf("%s: not listed", subject)
			continue
		}
		if got.AccessType != w.accessType {
			t.Errorf("%s access_type: want %q, got %q", subject, w.accessType, got.AccessType)
		}
		if !slices.Equal([]string(got.Groups), w.groups) {
			t.Errorf("%s groups: want %v, got %v", subject, w.groups, got.Groups)
		}
		if !slices.Equal([]string(got.AttributeGroups), w.attributeGroups) {
			t.Errorf("%s attribute_groups: want %v, got %v", subject, w.attributeGroups, got.AttributeGroups)
		}
	}

	// Ordered by email: u-admin, u-auditor, u-both, u-dba, u-sre.
	items, total, err := models.ListConnectionUsers(models.DB, testOrgID, "target", 2, 2)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if total != 5 || len(items) != 2 || items[0].Subject != "u-both" || items[1].Subject != "u-dba" {
		t.Errorf("page 2: want u-both,u-dba of 5, got %d of %d: %+v", len(items), total, items)
	}
	// A page past the end still reports the total, so the client can step back.
	items, total, err = models.ListConnectionUsers(models.DB, testOrgID, "target", 4, 2)
	if err != nil {
		t.Fatalf("page 4: %v", err)
	}
	if total != 5 || len(items) != 0 {
		t.Errorf("page 4: want 0 of 5, got %d of %d", len(items), total)
	}
}
