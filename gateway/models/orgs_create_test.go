package models_test

import (
	"testing"

	"github.com/hoophq/hoop/gateway/models"
)

// ORG_ID picks the id of the default organization on a fresh database. An
// existing organization keeps its id, whatever the caller asks for.
func TestCreateOrgGetOrganizationWithID(t *testing.T) {
	startTestDB(t)
	const wantID = "00000000-0000-0000-0000-0000000000b1"

	org, isNew, err := models.CreateOrgGetOrganizationWithID(wantID, "default", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !isNew || org.ID != wantID || org.Name != "default" {
		t.Fatalf("create: isNew=%v id=%q name=%q", isNew, org.ID, org.Name)
	}

	again, isNew, err := models.CreateOrgGetOrganizationWithID("00000000-0000-0000-0000-0000000000b2", "default", nil)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if isNew || again.ID != wantID {
		t.Fatalf("second call: isNew=%v id=%q, want existing %q", isNew, again.ID, wantID)
	}
}
