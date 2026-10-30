package models_test

import (
	"context"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The migration before and the one under test, stepped to by number.
const (
	beforeRiskLevelVersion = 146
	riskLevelVersion       = 147
)

// Migration 000147 adds reviews.risk_level and its dedup index, and the down
// removes both. No other column comes or goes.
func TestReviewRiskLevelMigration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { inst.Close(ctx) })

	riskLevelColumn := func() int64 {
		t.Helper()
		var n int64
		require.NoError(t, models.DB.Raw(`SELECT count(*) FROM information_schema.columns
			WHERE table_schema = 'private' AND table_name = 'reviews' AND column_name = 'risk_level'`).Scan(&n).Error)
		return n
	}
	reviewColumns := func() int64 {
		t.Helper()
		var n int64
		require.NoError(t, models.DB.Raw(`SELECT count(*) FROM information_schema.columns
			WHERE table_schema = 'private' AND table_name = 'reviews'`).Scan(&n).Error)
		return n
	}
	levelIndex := func() int64 {
		t.Helper()
		var n int64
		require.NoError(t, models.DB.Raw(`SELECT count(*) FROM pg_indexes WHERE schemaname = 'private'
			AND indexname = 'index_reviews_sidecar_level_statement'`).Scan(&n).Error)
		return n
	}
	sidecarID := uuid.NewString()
	// A live sidecar review filed under a risk level, as the plane writes it.
	// In its own transaction: a refused insert rolls back and leaves the
	// session usable.
	insertLevelReview := func(status, level string) error {
		return models.DB.Transaction(func(tx *gorm.DB) error {
			return tx.Exec(`INSERT INTO private.reviews (id, org_id, connection_name, type, status, owner_id,
				owner_email, sidecar_id, listener_name, risk_level, statement_hash)
				VALUES (?, ?, '', 'onetime', ?, 'u', 'hoop@hoop.dev', ?, 'appdb', ?, 'hash-1')`,
				uuid.NewString(), testOrgID, status, sidecarID, level).Error
		})
	}

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(beforeRiskLevelVersion) })
	var columnsBefore int64
	withDB(t, inst, func() {
		execSQL(t, `INSERT INTO private.orgs (id, name) VALUES (?, 'risk-level-migration')`, testOrgID)
		execSQL(t, `INSERT INTO private.sidecars (id, org_id, name, key_hash, created_by)
			VALUES (?, ?, 'edge', 'risk-level-key', 'tests@hoop.dev')`, sidecarID, testOrgID)
		// A review filed before the column existed.
		execSQL(t, `INSERT INTO private.reviews (id, org_id, connection_name, type, status, owner_id, owner_email, listener_name)
			VALUES (?, ?, '', 'onetime', 'PENDING', 'u', 'hoop@hoop.dev', 'old-lane')`, uuid.NewString(), testOrgID)
		assert.Zero(t, riskLevelColumn())
		assert.Zero(t, levelIndex())
		columnsBefore = reviewColumns()
	})

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(riskLevelVersion) })
	withDB(t, inst, func() {
		assert.EqualValues(t, 1, riskLevelColumn())
		assert.Equal(t, columnsBefore+1, reviewColumns(), "the up adds risk_level and nothing else")
		assert.EqualValues(t, 1, levelIndex())

		var level *string
		require.NoError(t, models.DB.Raw(`SELECT risk_level FROM private.reviews WHERE org_id = ? AND listener_name = 'old-lane'`,
			testOrgID).Row().Scan(&level))
		assert.Nil(t, level, "an existing review has no risk level")

		assert.Error(t, insertLevelReview("PENDING", "urgent"), "the check refuses a level that is not high, medium or low")
		require.NoError(t, insertLevelReview("PENDING", "high"))
		assert.Error(t, insertLevelReview("PENDING", "high"), "one live review per listener, level and statement")
		require.NoError(t, insertLevelReview("PENDING", "medium"), "another level is another key")
		execSQL(t, `UPDATE private.reviews SET status = 'EXECUTED' WHERE org_id = ? AND risk_level = 'high'`, testOrgID)
		require.NoError(t, insertLevelReview("PENDING", "high"), "a spent review does not block the next one")
	})

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(beforeRiskLevelVersion) })
	withDB(t, inst, func() {
		assert.Zero(t, riskLevelColumn())
		assert.Equal(t, columnsBefore, reviewColumns(), "the down removes risk_level and nothing else")
		assert.Zero(t, levelIndex())
		var n int64
		require.NoError(t, models.DB.Raw(`SELECT count(*) FROM private.reviews WHERE org_id = ?`, testOrgID).Scan(&n).Error)
		assert.EqualValues(t, 4, n, "the down deletes no review")
	})

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(riskLevelVersion) })
	withDB(t, inst, func() {
		assert.EqualValues(t, 1, riskLevelColumn())
		assert.EqualValues(t, 1, levelIndex())
	})
}
