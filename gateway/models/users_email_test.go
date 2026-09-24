package models_test

import (
	"testing"

	"github.com/hoophq/hoop/gateway/models"
)

// A Slack approval names its approver by email, so the lookup must ignore
// case, skip anyone neither active nor invited, and return a duplicate
// instead of hiding it.
func TestListApproverUsersByEmailAndOrg(t *testing.T) {
	startTestDB(t)

	for _, u := range []struct{ subject, email, status string }{
		{"ana", "Ana@Example.com", "active"},
		{"bob-old", "bob@example.com", "inactive"},
		{"carla-1", "carla@example.com", "active"},
		{"carla-2", "CARLA@example.com", "active"},
		{"dan", "dan@example.com", "invited"},
	} {
		err := models.DB.Exec(`
			INSERT INTO private.users (org_id, subject, email, name, status)
			VALUES (?, ?, ?, ?, ?)`, testOrgID, u.subject, u.email, u.subject, u.status).Error
		if err != nil {
			t.Fatalf("seed user %s: %v", u.subject, err)
		}
	}

	for _, tt := range []struct {
		email string
		want  int
	}{
		{"ana@example.com", 1},
		{"ANA@EXAMPLE.COM", 1},
		{"bob@example.com", 0},
		{"carla@example.com", 2},
		{"dan@example.com", 1},
		{"nobody@example.com", 0},
	} {
		got, err := models.ListApproverUsersByEmailAndOrg(models.DB, testOrgID, tt.email)
		if err != nil {
			t.Fatalf("%s: %v", tt.email, err)
		}
		if len(got) != tt.want {
			t.Errorf("%s: got %d users, want %d", tt.email, len(got), tt.want)
		}
	}

	got, err := models.ListApproverUsersByEmailAndOrg(models.DB, "00000000-0000-0000-0000-0000000000ff", "ana@example.com")
	if err != nil || len(got) != 0 {
		t.Errorf("other org: got %d users, err %v; want none", len(got), err)
	}
}
