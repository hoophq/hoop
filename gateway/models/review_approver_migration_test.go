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
)

// The migration before and the one under test, stepped to by number.
const (
	beforeApproverVersion = 145
	approverVersion       = 146
)

// Migration 000146 changes the schema only. An older gateway replica decodes
// sidecar_spec strictly, so the stored specs must keep every byte, and the
// review columns must come and go with the migration.
func TestRiskLevelApproverMigrationIsSchemaOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { inst.Close(ctx) })

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(beforeApproverVersion) })

	specs := map[string]string{
		// A hold under the rule's own managed rule: the form the new code
		// still reads, and the old one too.
		"own-hold": `{"high":"require_review","medium":"require_approval","low":"warn","approval_rule":"own-hold","review_mode":"return"}`,
		// Under a hand-made rule.
		"other-hold": `{"high":"require_review","approval_rule":"hand-made"}`,
		// Names its own name with no managed rule behind it.
		"orphan":  `{"high":"require_review","approval_rule":"orphan"}`,
		"no-hold": `{"high":"block","trigger":{"operations":["delete"]}}`,
	}
	storedSpecs := func() map[string]string {
		t.Helper()
		var rows []struct {
			Name string `gorm:"column:name"`
			Spec string `gorm:"column:spec"`
		}
		require.NoError(t, models.DB.Raw(`SELECT name, sidecar_spec::text AS spec FROM private.ai_session_analyzer_rules
			WHERE org_id = ? ORDER BY name`, testOrgID).Scan(&rows).Error)
		out := map[string]string{}
		for _, r := range rows {
			out[r.Name] = r.Spec
		}
		return out
	}
	reviewColumns := func() int64 {
		t.Helper()
		var n int64
		require.NoError(t, models.DB.Raw(`SELECT count(*) FROM information_schema.columns
			WHERE table_schema = 'private' AND table_name = 'reviews'
			AND column_name IN ('approver_type', 'decision_reason', 'risk_level')`).Scan(&n).Error)
		return n
	}

	var before map[string]string
	withDB(t, inst, func() {
		execSQL(t, `INSERT INTO private.orgs (id, name) VALUES (?, 'approver-migration')`, testOrgID)
		execSQL(t, `INSERT INTO private.access_request_rules (org_id, name, connection_names, access_type,
			approval_required_groups, reviewers_groups, force_approval_groups, min_approvals, managed_by)
			VALUES (?, 'own-hold', '{}', 'sidecar', '{}', '{dba,sre}', '{}', 1, 'ai-session-analyzer'),
			       (?, 'hand-made', '{}', 'sidecar', '{}', '{ops}', '{}', 1, NULL)`, testOrgID, testOrgID)
		for name, spec := range specs {
			execSQL(t, `INSERT INTO private.ai_session_analyzer_rules (org_id, name, connection_names, risk_evaluation, sidecar_spec)
				VALUES (?, ?, '{}', '{}'::jsonb, ?::jsonb)`, testOrgID, name, spec)
		}
		// A review filed before the columns existed.
		execSQL(t, `INSERT INTO private.reviews (id, org_id, connection_name, type, status, owner_id, owner_email, listener_name)
			VALUES (?, ?, '', 'onetime', 'PENDING', 'u', 'hoop@hoop.dev', 'old-lane')`, uuid.NewString(), testOrgID)
		before = storedSpecs()
		require.Len(t, before, len(specs))
		assert.Zero(t, reviewColumns())
	})

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(approverVersion) })

	withDB(t, inst, func() {
		assert.Equal(t, before, storedSpecs(), "the up migration rewrites no sidecar_spec")
		assert.EqualValues(t, 3, reviewColumns())

		var approver string
		require.NoError(t, models.DB.Raw(`SELECT approver_type FROM private.reviews WHERE org_id = ? AND listener_name = 'old-lane'`,
			testOrgID).Row().Scan(&approver))
		assert.Equal(t, models.ApproverTypeHuman, approver, "an existing review reads as decided by a person")

		// An agent decision, as the plane files it.
		execSQL(t, `INSERT INTO private.reviews (id, org_id, connection_name, type, status, owner_id, owner_email,
			listener_name, approver_type, decision_reason, risk_level)
			VALUES (?, ?, '', 'onetime', 'APPROVED', 'u', 'hoop@hoop.dev', 'appdb', 'ai_agent', 'bounded', 'high')`,
			uuid.NewString(), testOrgID)
		// A spec the new UI wrote: the down leaves it as written.
		execSQL(t, `UPDATE private.ai_session_analyzer_rules SET sidecar_spec = ?::jsonb WHERE org_id = ? AND name = 'no-hold'`,
			`{"high":{"action":"require_approval","approver":"human","approval_groups":["dba"]}}`, testOrgID)
		before = storedSpecs()
	})

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(beforeApproverVersion) })

	withDB(t, inst, func() {
		assert.Equal(t, before, storedSpecs(), "the down migration rewrites no sidecar_spec")
		assert.Zero(t, reviewColumns())
		var status string
		require.NoError(t, models.DB.Raw(`SELECT status FROM private.reviews WHERE org_id = ? AND listener_name = 'appdb'`,
			testOrgID).Row().Scan(&status))
		assert.Equal(t, "EXECUTED", status, "an older binary must not release an agent approval")
		var index int64
		require.NoError(t, models.DB.Raw(`SELECT count(*) FROM pg_indexes WHERE schemaname = 'private'
			AND indexname = 'index_reviews_sidecar_level_statement'`).Scan(&index).Error)
		assert.Zero(t, index)
	})

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(approverVersion) })

	withDB(t, inst, func() {
		assert.Equal(t, before, storedSpecs())
		assert.EqualValues(t, 3, reviewColumns())
		var index int64
		require.NoError(t, models.DB.Raw(`SELECT count(*) FROM pg_indexes WHERE schemaname = 'private'
			AND indexname = 'index_reviews_sidecar_level_statement'`).Scan(&index).Error)
		assert.EqualValues(t, 1, index)
	})
}
