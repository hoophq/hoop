package models_test

import (
	"testing"

	"github.com/hoophq/hoop/gateway/models"
)

// A refusal is held on the row until an apply replaces it. Two handshakes
// would otherwise clear it: a build from before ADR-0022 reports "unchanged"
// on the tick after the refusal and adopts the served revision, and a boot on
// a refused document reports nothing at all before it exits.
func TestARefusalIsHeldUntilAnApply(t *testing.T) {
	startTestDB(t)

	sc := &models.Sidecar{
		OrgID:     testOrgID,
		Name:      "latch",
		KeyHash:   models.HashAPIKey("hsc_latch_test"),
		CreatedBy: "tests@hoop.dev",
	}
	if err := models.CreateSidecar(models.DB, sc); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}
	record := func(applied, outcome, reason, served string) *models.Sidecar {
		t.Helper()
		if err := models.RecordSidecarHandshake(models.DB, sc.ID, "1.2.3", applied, outcome, reason, served, []string{"review_mode"}, nil); err != nil {
			t.Fatalf("record: %v", err)
		}
		got, err := models.GetSidecarByNameOrID(models.DB, testOrgID, sc.Name)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		return got
	}
	state := func(got *models.Sidecar) [3]string {
		s := func(p *string) string {
			if p == nil {
				return "<nil>"
			}
			return *p
		}
		return [3]string{s(got.AppliedRevision), s(got.LastOutcome), s(got.LastError)}
	}

	got := record("rev-1", "refused", "boom", "rev-2")
	if want := [3]string{"rev-1", "refused", "boom"}; state(got) != want {
		t.Fatalf("after the refusal: %v, want %v", state(got), want)
	}
	if got.ServedRevisionAt == nil {
		t.Fatal("served_revision_at was not stamped")
	}
	servedAt := *got.ServedRevisionAt

	// An older build decays the refusal into "unchanged" and claims rev-2.
	got = record("rev-2", "unchanged", "", "rev-2")
	if want := [3]string{"rev-1", "refused", "boom"}; state(got) != want {
		t.Errorf("an unchanged report cleared the refusal: %v, want %v", state(got), want)
	}
	if !got.ServedRevisionAt.Equal(servedAt) {
		t.Errorf("served_revision_at moved on an unchanged revision")
	}
	// A boot on the refused document reports nothing before it exits.
	got = record("", "", "", "rev-2")
	if want := [3]string{"rev-1", "refused", "boom"}; state(got) != want {
		t.Errorf("a boot handshake cleared the refusal: %v, want %v", state(got), want)
	}
	// The sidecar takes a document: the hold ends.
	got = record("rev-3", "applied", "", "rev-3")
	if want := [3]string{"rev-3", "applied", "<nil>"}; state(got) != want {
		t.Errorf("an apply did not replace the refusal: %v, want %v", state(got), want)
	}
	if !got.ServedRevisionAt.After(servedAt) {
		t.Errorf("served_revision_at did not move with the revision")
	}
	// An unchanged report after an apply is the steady state, and a boot
	// after it keeps what was applied rather than forgetting it.
	got = record("rev-3", "unchanged", "", "rev-3")
	if want := [3]string{"rev-3", "unchanged", "<nil>"}; state(got) != want {
		t.Errorf("the steady state was not recorded: %v, want %v", state(got), want)
	}
	got = record("", "", "", "rev-3")
	if want := [3]string{"rev-3", "unchanged", "<nil>"}; state(got) != want {
		t.Errorf("a boot handshake forgot the applied revision: %v, want %v", state(got), want)
	}
}
