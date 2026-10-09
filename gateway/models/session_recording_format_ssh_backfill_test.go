package models_test

import (
	"context"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite"
)

const sshRecordingFormatVersion = 139

func TestBackfillSSHRecordingFormat(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("start embedded database: %v", err)
	}
	t.Cleanup(func() { inst.Close(ctx) })

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(sshRecordingFormatVersion - 1) })

	type row struct {
		id, connType, subtype, verb string
		format, origin              any
	}
	seed := []row{
		{"00000000-0000-0000-0000-0000000005a1", "application", "ssh", "connect", "raw", "client"},
		{"00000000-0000-0000-0000-0000000005a2", "application", "ssh-local", "connect", "raw", nil},
		{"00000000-0000-0000-0000-0000000005a3", "application", "git", "connect", "raw", nil},
		{"00000000-0000-0000-0000-0000000005a4", "application", "github", "connect", "raw", nil},
		// Not SSH frames: keep raw.
		{"00000000-0000-0000-0000-0000000005b1", "application", "tcp", "connect", "raw", nil},
		{"00000000-0000-0000-0000-0000000005b2", "custom", "ssh", "connect", "raw", nil},
		// A sidecar records statements on its SSH listener's mirror connection.
		{"00000000-0000-0000-0000-0000000005b5", "application", "ssh", "connect", "raw", "sidecar"},
		// Recorded before 000126: the viewer selects these by type.
		{"00000000-0000-0000-0000-0000000005b3", "application", "ssh", "connect", nil, nil},
		{"00000000-0000-0000-0000-0000000005b4", "application", "ssh", "exec", "exec", nil},
	}
	withDB(t, inst, func() {
		execSQL(t, `INSERT INTO private.orgs (id, name) VALUES (?, 'ssh-format-backfill')`, testOrgID)
		for _, r := range seed {
			execSQL(t, `INSERT INTO private.sessions (id, org_id, connection, connection_type, connection_subtype, verb, status, created_at, recording_format, origin)
				VALUES (?, ?, 'conn', ?, ?, ?, 'done', now(), ?, ?)`, r.id, testOrgID, r.connType, r.subtype, r.verb, r.format, r.origin)
		}
	})

	// The embedded backend keeps one server session across pools, so each
	// phase reads with its own query text: the driver would otherwise prepare
	// a statement name that the previous pool left behind.
	assertFormats := func(phase string, want map[string]string) {
		t.Helper()
		withDB(t, inst, func() {
			var rows []struct{ ID, Format string }
			err := models.DB.Raw(`SELECT id, COALESCE(recording_format, '') AS format
				FROM private.sessions WHERE org_id = ? -- `+phase, testOrgID).Scan(&rows).Error
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			for _, r := range rows {
				got[r.ID] = r.Format
			}
			for id, format := range want {
				if got[id] != format {
					t.Errorf("%s: session %s format = %q, want %q", phase, id, got[id], format)
				}
			}
		})
	}

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(sshRecordingFormatVersion) })
	assertFormats("up", map[string]string{
		"00000000-0000-0000-0000-0000000005a1": "ssh",
		"00000000-0000-0000-0000-0000000005a2": "ssh",
		"00000000-0000-0000-0000-0000000005a3": "ssh",
		"00000000-0000-0000-0000-0000000005a4": "ssh",
		"00000000-0000-0000-0000-0000000005b1": "raw",
		"00000000-0000-0000-0000-0000000005b2": "raw",
		"00000000-0000-0000-0000-0000000005b5": "raw",
		"00000000-0000-0000-0000-0000000005b3": "",
		"00000000-0000-0000-0000-0000000005b4": "exec",
	})

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(sshRecordingFormatVersion - 1) })
	assertFormats("down", map[string]string{
		"00000000-0000-0000-0000-0000000005a1": "raw",
		"00000000-0000-0000-0000-0000000005a2": "raw",
		"00000000-0000-0000-0000-0000000005a3": "raw",
		"00000000-0000-0000-0000-0000000005a4": "raw",
		"00000000-0000-0000-0000-0000000005b1": "raw",
		"00000000-0000-0000-0000-0000000005b2": "raw",
		"00000000-0000-0000-0000-0000000005b5": "raw",
		"00000000-0000-0000-0000-0000000005b3": "",
		"00000000-0000-0000-0000-0000000005b4": "exec",
	})
}
