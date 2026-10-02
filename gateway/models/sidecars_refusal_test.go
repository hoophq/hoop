package models_test

import (
	"testing"

	"github.com/hoophq/hoop/gateway/models"
)

// A handshake the plane refuses to serve is recorded with its reason, so the
// page shows why, and without a fresh last_seen_at, so a sidecar that cannot
// run does not read as recently seen.
func TestAServeRefusalIsRecordedWithItsReason(t *testing.T) {
	startTestDB(t)

	sc := &models.Sidecar{
		OrgID:     testOrgID,
		Name:      "refused-at-serve",
		KeyHash:   models.HashAPIKey("hsc_refused_at_serve_test"),
		CreatedBy: "tests@hoop.dev",
	}
	if err := models.CreateSidecar(models.DB, sc); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}
	reason := `listener "web" sets http.sensitive_query_params, and this sidecar (1.184.1) does not support it; upgrade the sidecar to 1.184.2 or later`
	if err := models.RecordSidecarServeRefusal(models.DB, sc.ID, "1.184.1", []string{}, reason); err != nil {
		t.Fatalf("record: %v", err)
	}
	got, err := models.GetSidecarByNameOrID(models.DB, testOrgID, sc.Name)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.LastSeenAt != nil {
		t.Error("a refused handshake stamped last_seen_at")
	}
	if got.ReportedVersion == nil || *got.ReportedVersion != "1.184.1" {
		t.Errorf("reported_version = %v, want 1.184.1", got.ReportedVersion)
	}
	if got.LastOutcome == nil || *got.LastOutcome != models.SidecarOutcomeNotServed {
		t.Errorf("last_outcome = %v, want %s", got.LastOutcome, models.SidecarOutcomeNotServed)
	}
	if got.LastError == nil || *got.LastError != reason {
		t.Errorf("last_error = %v, want the reason", got.LastError)
	}
	if got.Capabilities == nil || len(got.Capabilities) != 0 {
		t.Errorf("capabilities = %v, want an empty array for a build with no header", got.Capabilities)
	}
}

// The plane's own refusal ends with the next handshake the plane serves,
// whatever the sidecar reports: it never received the refused document, so
// an "unchanged" report, or none from a build too old to report, must not
// hold it the way a refusal the sidecar reported is held.
func TestAServeRefusalEndsWithTheNextServedHandshake(t *testing.T) {
	startTestDB(t)

	sc := &models.Sidecar{
		OrgID:     testOrgID,
		Name:      "refused-then-served",
		KeyHash:   models.HashAPIKey("hsc_refused_then_served_test"),
		CreatedBy: "tests@hoop.dev",
	}
	if err := models.CreateSidecar(models.DB, sc); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}
	refuse := func() {
		t.Helper()
		if err := models.RecordSidecarServeRefusal(models.DB, sc.ID, "1.190.0", []string{}, "too old"); err != nil {
			t.Fatalf("refuse: %v", err)
		}
	}
	serve := func(applied, outcome string) *models.Sidecar {
		t.Helper()
		if err := models.RecordSidecarHandshake(models.DB, sc.ID, "1.190.0", applied, outcome, "", "rev-1", []string{}, nil); err != nil {
			t.Fatalf("serve: %v", err)
		}
		got, err := models.GetSidecarByNameOrID(models.DB, testOrgID, sc.Name)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		return got
	}

	refuse()
	got := serve("rev-1", "unchanged")
	if got.LastOutcome == nil || *got.LastOutcome != "unchanged" || got.LastError != nil {
		t.Errorf("an unchanged report did not end the plane's refusal: outcome=%v error=%v", got.LastOutcome, got.LastError)
	}
	if got.LastSeenAt == nil {
		t.Error("a served handshake did not stamp last_seen_at")
	}

	refuse()
	got = serve("", "")
	if got.LastOutcome != nil || got.LastError != nil {
		t.Errorf("a handshake that reports nothing did not end the plane's refusal: outcome=%v error=%v", got.LastOutcome, got.LastError)
	}
}
