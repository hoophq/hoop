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
	if got.LastOutcome == nil || *got.LastOutcome != "refused" {
		t.Errorf("last_outcome = %v, want refused", got.LastOutcome)
	}
	if got.LastError == nil || *got.LastError != reason {
		t.Errorf("last_error = %v, want the reason", got.LastError)
	}
	if got.Capabilities == nil || len(got.Capabilities) != 0 {
		t.Errorf("capabilities = %v, want an empty array for a build with no header", got.Capabilities)
	}
}
