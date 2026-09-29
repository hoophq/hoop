package models_test

import (
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/models"
)

func TestUpsertSessionRecordingFormat(t *testing.T) {
	startTestDB(t)

	newSession := models.Session{
		ID:                "00000000-0000-0000-0000-0000000000a2",
		OrgID:             testOrgID,
		Connection:        "k8s",
		ConnectionType:    "custom",
		ConnectionSubtype: "kubernetes",
		Verb:              "connect",
		Status:            "open",
		CreatedAt:         time.Now().UTC(),
	}
	if err := models.UpsertSession(newSession); err != nil {
		t.Fatalf("create new session: %v", err)
	}
	assertRecordingFormat(t, newSession.ID, "raw")
	if err := models.UpsertSession(newSession); err != nil {
		t.Fatalf("update new session: %v", err)
	}
	assertRecordingFormat(t, newSession.ID, "raw")

	legacy := newSession
	legacy.ID = "00000000-0000-0000-0000-0000000000a3"
	if err := models.DB.Exec(`
		INSERT INTO private.sessions (id, org_id, connection, connection_type, connection_subtype, verb, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, legacy.ID, legacy.OrgID, legacy.Connection,
		legacy.ConnectionType, legacy.ConnectionSubtype, legacy.Verb, legacy.Status, legacy.CreatedAt).Error; err != nil {
		t.Fatalf("seed historical session: %v", err)
	}
	if err := models.UpsertSession(legacy); err != nil {
		t.Fatalf("update historical session: %v", err)
	}
	assertRecordingFormat(t, legacy.ID, "")
}

func assertRecordingFormat(t *testing.T, id, want string) {
	t.Helper()
	var row struct{ RecordingFormat *string }
	if err := models.DB.Table("private.sessions").Select("recording_format").Where("id = ? AND org_id = ?", id, testOrgID).Take(&row).Error; err != nil {
		t.Fatalf("read session recording format: %v", err)
	}
	if row.RecordingFormat == nil {
		if want != "" {
			t.Fatalf("recording format is NULL, want %q", want)
		}
	} else if *row.RecordingFormat != want {
		t.Fatalf("recording format = %q, want %q", *row.RecordingFormat, want)
	}
}
