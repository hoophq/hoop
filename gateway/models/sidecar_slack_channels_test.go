package models_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/lib/pq"
)

// A listener's channels replace the sidecar's, the sidecar's apply to every
// other listener, and a listener that is gone takes its channels with it.
func TestSidecarSlackChannels(t *testing.T) {
	startTestDB(t)
	sidecarID := uuid.NewString()
	if err := models.DB.Exec(`INSERT INTO private.sidecars (id, org_id, name, key_hash, created_by)
		VALUES (?, ?, 'payments', 'hash-1', 'admin@example.com')`, sidecarID, testOrgID).Error; err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}

	resolve := func(listener string) []string {
		t.Helper()
		got, err := models.ResolveSidecarSlackChannels(models.DB, testOrgID, sidecarID, listener)
		if err != nil {
			t.Fatalf("resolve %q: %v", listener, err)
		}
		return got
	}

	if got := resolve("pg"); len(got) != 0 {
		t.Fatalf("nothing set: got %v, want none", got)
	}

	err := models.ReplaceSidecarSlackChannels(models.DB, testOrgID, sidecarID, []models.SidecarSlackChannels{
		{ListenerName: "", Channels: pq.StringArray{"C-SIDECAR"}},
		{ListenerName: "pg", Channels: pq.StringArray{"C-PG", "C-DBA"}},
		{ListenerName: "mysql", Channels: pq.StringArray{}},
	})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if got := resolve("pg"); !slices.Equal(got, []string{"C-PG", "C-DBA"}) {
		t.Errorf("pg = %v, want its own channels", got)
	}
	if got := resolve("mysql"); !slices.Equal(got, []string{"C-SIDECAR"}) {
		t.Errorf("mysql = %v, want the sidecar's: an empty list inherits", got)
	}
	if got := resolve(""); !slices.Equal(got, []string{"C-SIDECAR"}) {
		t.Errorf("no listener = %v, want the sidecar's", got)
	}

	// The configuration dropped "pg": its row goes, the sidecar's stays.
	if err := models.PruneSidecarSlackChannels(models.DB, testOrgID, sidecarID, []string{"mysql"}); err != nil {
		t.Fatalf("prune: %v", err)
	}
	rows, err := models.ListSidecarSlackChannels(models.DB, testOrgID, sidecarID)
	if err != nil || len(rows) != 1 || rows[0].ListenerName != "" {
		t.Fatalf("after prune: %+v err %v; want only the sidecar row", rows, err)
	}

	// Deleting the sidecar cascades.
	if err := models.DB.Exec(`DELETE FROM private.sidecars WHERE id = ?`, sidecarID).Error; err != nil {
		t.Fatalf("delete sidecar: %v", err)
	}
	if rows, _ := models.ListSidecarSlackChannels(models.DB, testOrgID, sidecarID); len(rows) != 0 {
		t.Fatalf("rows survived the sidecar: %+v", rows)
	}
}
