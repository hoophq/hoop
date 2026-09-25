package models_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/lib/pq"
)

// Each listener has its own channels, and a listener that is gone takes its
// channels with it.
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

	err := models.SetSidecarSlackChannels(models.DB, testOrgID, sidecarID, []models.SidecarSlackChannels{
		{ListenerName: "pg", Channels: pq.StringArray{"C-PG", "C-DBA"}},
		{ListenerName: "mysql", Channels: pq.StringArray{"C-MYSQL"}},
		{ListenerName: "redis", Channels: pq.StringArray{}},
	})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := resolve("pg"); !slices.Equal(got, []string{"C-PG", "C-DBA"}) {
		t.Errorf("pg = %v, want its own channels", got)
	}
	if got := resolve("redis"); len(got) != 0 {
		t.Errorf("redis = %v, want none: an empty list is no row", got)
	}
	if got := resolve(""); len(got) != 0 {
		t.Errorf("no listener = %v, want none", got)
	}

	// A second admin saves only mysql: pg keeps its channels.
	if err := models.SetSidecarSlackChannels(models.DB, testOrgID, sidecarID, []models.SidecarSlackChannels{
		{ListenerName: "mysql", Channels: pq.StringArray{"C-MYSQL-2"}},
	}); err != nil {
		t.Fatalf("set mysql: %v", err)
	}
	if got := resolve("pg"); !slices.Equal(got, []string{"C-PG", "C-DBA"}) {
		t.Errorf("pg = %v after a mysql save, want it untouched", got)
	}
	if got := resolve("mysql"); !slices.Equal(got, []string{"C-MYSQL-2"}) {
		t.Errorf("mysql = %v, want the new channel", got)
	}

	// The configuration dropped "pg": its row goes, mysql's stays.
	if err := models.PruneSidecarSlackChannels(models.DB, testOrgID, sidecarID, []string{"mysql"}); err != nil {
		t.Fatalf("prune: %v", err)
	}
	rows, err := models.ListSidecarSlackChannels(models.DB, testOrgID, sidecarID)
	if err != nil || len(rows) != 1 || rows[0].ListenerName != "mysql" {
		t.Fatalf("after prune: %+v err %v; want only the mysql row", rows, err)
	}

	// Deleting the sidecar cascades.
	if err := models.DB.Exec(`DELETE FROM private.sidecars WHERE id = ?`, sidecarID).Error; err != nil {
		t.Fatalf("delete sidecar: %v", err)
	}
	if rows, _ := models.ListSidecarSlackChannels(models.DB, testOrgID, sidecarID); len(rows) != 0 {
		t.Fatalf("rows survived the sidecar: %+v", rows)
	}
}
