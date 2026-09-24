package models_test

import (
	"testing"

	"github.com/hoophq/hoop/gateway/models"
)

// TestRecordSidecarHandshake covers the fleet-state columns end to end against
// a real schema: the migration, the UPDATE, and the SELECT list that reads
// them back. A column named wrongly in any of the three fails here rather than
// in a fleet view that quietly reports every sidecar as unknown.
func TestRecordSidecarHandshake(t *testing.T) {
	startTestDB(t)

	sc := &models.Sidecar{
		OrgID:     testOrgID,
		Name:      "runtime-state",
		KeyHash:   models.HashAPIKey("hsc_runtime_state_test"),
		CreatedBy: "tests@hoop.dev",
	}
	if err := models.CreateSidecar(models.DB, sc); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}

	// Never handshaken: every column is NULL, which must read as unknown
	// rather than as a converged sidecar.
	fresh, err := models.GetSidecarByNameOrID(models.DB, testOrgID, sc.Name)
	if err != nil {
		t.Fatalf("read the seeded sidecar: %v", err)
	}
	if fresh.LastSeenAt != nil || fresh.ReportedVersion != nil ||
		fresh.ServedRevision != nil || fresh.AppliedRevision != nil || fresh.LastOutcome != nil {
		t.Fatalf("a sidecar that never handshook must carry no runtime state, got %+v", fresh)
	}

	const revision = "8f14e45fceea167a5a36dedd4bea2543"
	if err := models.RecordSidecarHandshake(models.DB, sc.ID, "1.2.3", "", "", revision,
		[]string{"listeners", "listeners.name"}, []string{"postgres"}); err != nil {
		t.Fatalf("record the first handshake: %v", err)
	}
	got, err := models.GetSidecarByNameOrID(models.DB, testOrgID, sc.Name)
	if err != nil {
		t.Fatalf("read back the first handshake: %v", err)
	}
	if got.LastSeenAt == nil {
		t.Error("last_seen_at was not stamped")
	}
	if got.ReportedVersion == nil || *got.ReportedVersion != "1.2.3" {
		t.Errorf("reported_version = %v, want 1.2.3", got.ReportedVersion)
	}
	if got.ServedRevision == nil || *got.ServedRevision != revision {
		t.Errorf("served_revision = %v, want %s", got.ServedRevision, revision)
	}
	if len(got.ReportedConfigKeys) != 2 || len(got.ReportedProtocols) != 1 || got.ReportedProtocols[0] != "postgres" {
		t.Errorf("reported support = %v / %v, want the two keys and postgres", got.ReportedConfigKeys, got.ReportedProtocols)
	}
	// The sidecar reported nothing about a previous document, so these stay
	// NULL. An empty string stored here would read as a real answer.
	if got.AppliedRevision != nil || got.LastOutcome != nil {
		t.Errorf("a first handshake reports no previous document, got applied=%v outcome=%v",
			got.AppliedRevision, got.LastOutcome)
	}

	// The next handshake reports what was done with that document.
	if err := models.RecordSidecarHandshake(models.DB, sc.ID, "1.2.3", revision, "applied", revision, nil, nil); err != nil {
		t.Fatalf("record the second handshake: %v", err)
	}
	got, err = models.GetSidecarByNameOrID(models.DB, testOrgID, sc.Name)
	if err != nil {
		t.Fatalf("read back the second handshake: %v", err)
	}
	if got.AppliedRevision == nil || *got.AppliedRevision != revision {
		t.Errorf("applied_revision = %v, want %s", got.AppliedRevision, revision)
	}
	if got.LastOutcome == nil || *got.LastOutcome != "applied" {
		t.Errorf("last_outcome = %v, want applied", got.LastOutcome)
	}
	// A build that reports nothing reads as unknown, not as the last list.
	if got.ReportedConfigKeys != nil || got.ReportedProtocols != nil {
		t.Errorf("a handshake reporting no support must clear it, got %v / %v", got.ReportedConfigKeys, got.ReportedProtocols)
	}
}
