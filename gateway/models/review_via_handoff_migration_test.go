package models_test

import (
	"context"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	migrationfiles "github.com/hoophq/hoop/gateway/migrations"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The migration that adds reviews.via_handoff, stepped to by number.
const viaHandoffVersion = 148

func TestReviewViaHandoffMigrationRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { inst.Close(ctx) })

	// The migration before it, read from the files: the sequence has gaps.
	src, err := iofs.New(migrationfiles.FS, ".")
	require.NoError(t, err)
	prev, err := src.Prev(viaHandoffVersion)
	require.NoError(t, err)
	require.NoError(t, src.Close())

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(prev) })

	levelIndexDef := func() string {
		t.Helper()
		var def string
		require.NoError(t, models.DB.Raw(`SELECT indexdef FROM pg_indexes
			WHERE schemaname = 'private' AND indexname = 'index_reviews_sidecar_level_statement'`).Row().Scan(&def))
		return def
	}

	var sidecarID, indexBefore string
	oldReview := uuid.NewString()
	withDB(t, inst, func() {
		indexBefore = levelIndexDef()
		execSQL(t, `INSERT INTO private.orgs (id, name) VALUES (?, 'via-handoff-migration')`, testOrgID)
		require.NoError(t, models.DB.Raw(`INSERT INTO private.sidecars (org_id, name, key_hash, created_by)
			VALUES (?, 'sc', 'via-handoff-key', 'x') RETURNING id`, testOrgID).Row().Scan(&sidecarID))
		execSQL(t, `INSERT INTO private.reviews (id, org_id, connection_name, type, status, owner_id, owner_email, listener_name)
			VALUES (?, ?, '', 'onetime', 'PENDING', 'u', 'hoop@hoop.dev', 'old-lane')`, oldReview, testOrgID)
	})
	assert.NotContains(t, indexBefore, "via_handoff")

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(viaHandoffVersion) })

	// A live level review, filed directly or through a hand-off.
	levelReview := func(viaHandoff bool, hash string) (reviewID, sessionID string) {
		t.Helper()
		reviewID, sessionID = uuid.NewString(), uuid.NewString()
		execSQL(t, `INSERT INTO private.sessions (id, org_id, connection, connection_type, verb, status)
			VALUES (?, ?, '', 'custom', 'exec', 'open')`, sessionID, testOrgID)
		execSQL(t, `INSERT INTO private.reviews (id, org_id, session_id, connection_name, type, status, owner_id, owner_email,
			sidecar_id, listener_name, risk_level, statement_hash, via_handoff)
			VALUES (?, ?, ?, '', 'onetime', 'PENDING', 'u', 'hoop@hoop.dev', ?, 'appdb', 'high', ?, ?)`,
			reviewID, testOrgID, sessionID, sidecarID, hash, viaHandoff)
		return reviewID, sessionID
	}
	status := func(table, id string) string {
		t.Helper()
		var s string
		require.NoError(t, models.DB.Raw(`SELECT status FROM private.`+table+` WHERE org_id = ? AND id = ?`,
			testOrgID, id).Row().Scan(&s))
		return s
	}

	var collidingHandoff, collidingSession, loneHandoff string
	withDB(t, inst, func() {
		var via bool
		require.NoError(t, models.DB.Raw(`SELECT via_handoff FROM private.reviews WHERE org_id = ? AND id = ?`,
			testOrgID, oldReview).Row().Scan(&via))
		assert.False(t, via, "an existing review was filed directly")

		// The same level and statement, filed both ways: two live reviews.
		levelReview(false, "same-hash")
		collidingHandoff, collidingSession = levelReview(true, "same-hash")
		loneHandoff, _ = levelReview(true, "other-hash")

		// The live-review index keys on via_handoff.
		assert.Contains(t, levelIndexDef(), "via_handoff")
	})

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(prev) })

	withDB(t, inst, func() {
		var columns int64
		require.NoError(t, models.DB.Raw(`SELECT count(*) FROM information_schema.columns
			WHERE table_schema = 'private' AND table_name = 'reviews' AND column_name = 'via_handoff'`).Scan(&columns).Error)
		assert.Zero(t, columns)

		// The 000147 index comes back as it was, and holds one live review
		// per level and statement.
		assert.Equal(t, indexBefore, levelIndexDef())
		assert.Equal(t, "EXPIRED", status("reviews", collidingHandoff),
			"the hand-off review beside a direct one expires")
		assert.Equal(t, "done", status("sessions", collidingSession), "its session closes")
		assert.Equal(t, "PENDING", status("reviews", loneHandoff), "a hand-off review alone stays live")
	})

	// And up again.
	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(viaHandoffVersion) })
	withDB(t, inst, func() {
		var via bool
		require.NoError(t, models.DB.Raw(`SELECT via_handoff FROM private.reviews WHERE org_id = ? AND id = ?`,
			testOrgID, loneHandoff).Row().Scan(&via))
		assert.False(t, via, "the down dropped the column; the row reads as filed directly")
	})
}
