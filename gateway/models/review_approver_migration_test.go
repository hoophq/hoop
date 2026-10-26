package models_test

import (
	"context"
	"encoding/json"
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

// Specs the down migration must leave in the object form.
const (
	splitGroups = `{"high":{"action":"require_approval","approver":"human","approval_groups":["dba"]},` +
		`"medium":{"action":"require_approval","approver":"human","approval_groups":["sre"]}}`
	mixedExternal = `{"high":{"action":"require_approval","approver":"human","approval_groups":["dba"]},` +
		`"medium":"require_review","approval_rule":"hand-made"}`
	staleRule = `{"high":{"action":"require_approval","approver":"human","approval_groups":["payments"]}}`
	// No groups is the administrators; the rule was edited to payments.
	adminLevel = `{"high":{"action":"require_approval","approver":"human"}}`
	// A hold object with no approver is the approval agent's.
	implicitAgent      = `{"high":{"action":"require_approval"},"medium":{"action":"require_review"}}`
	implicitAgentMixed = `{"high":{"action":"require_approval","approver":"human","approval_groups":["dba"]},` +
		`"medium":{"action":"require_approval"}}`
	explicitAgentMixed = `{"high":{"action":"require_approval","approver":"human","approval_groups":["dba"]},` +
		`"low":{"action":"require_approval","approver":"ai_agent"}}`
	// The managed rule matches the level's groups and adds a force approver.
	forceGroup = `{"high":{"action":"require_approval","approver":"human","approval_groups":["dba"]}}`
)

func specJSON(t *testing.T, spec map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(spec)
	require.NoError(t, err)
	return string(raw)
}

func TestRiskLevelApproverMigrationRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { inst.Close(ctx) })

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(beforeApproverVersion) })

	readSpec := func(name string) map[string]any {
		var raw string
		require.NoError(t, models.DB.Raw(`SELECT sidecar_spec::text FROM private.ai_session_analyzer_rules
			WHERE org_id = ? AND name = ?`, testOrgID, name).Row().Scan(&raw))
		out := map[string]any{}
		require.NoError(t, json.Unmarshal([]byte(raw), &out))
		return out
	}
	analyzerRule := func(name, spec string) {
		execSQL(t, `INSERT INTO private.ai_session_analyzer_rules (org_id, name, connection_names, risk_evaluation, sidecar_spec)
			VALUES (?, ?, '{}', '{}'::jsonb, ?::jsonb)`, testOrgID, name, spec)
	}
	accessRule := func(name, managedBy, groups string) {
		execSQL(t, `INSERT INTO private.access_request_rules (org_id, name, connection_names, access_type,
			approval_required_groups, reviewers_groups, force_approval_groups, min_approvals, managed_by)
			VALUES (?, ?, '{}', 'sidecar', '{}', ?::text[], '{}', 1, NULLIF(?, ''))`, testOrgID, name, groups, managedBy)
	}
	// forceRule is a managed rule an admin gave a force approver.
	forceRule := func(name, groups string) {
		execSQL(t, `INSERT INTO private.access_request_rules (org_id, name, connection_names, access_type,
			approval_required_groups, reviewers_groups, force_approval_groups, min_approvals, managed_by)
			VALUES (?, ?, '{}', 'sidecar', '{}', ?::text[], '{security}', 1, 'ai-session-analyzer')`, testOrgID, name, groups)
	}

	withDB(t, inst, func() {
		execSQL(t, `INSERT INTO private.orgs (id, name) VALUES (?, 'approver-migration')`, testOrgID)
		// Held under its own managed rule: rewritten to human levels.
		accessRule("own-hold", "ai-session-analyzer", "{dba,sre}")
		analyzerRule("own-hold", `{"high":"require_review","medium":"require_approval","low":"warn","approval_rule":"own-hold","review_mode":"return"}`)
		// Held under a rule an admin made: left alone.
		accessRule("hand-made", "", "{ops}")
		analyzerRule("other-hold", `{"high":"require_review","approval_rule":"hand-made"}`)
		// Names its own name with no managed rule behind it: left alone.
		analyzerRule("orphan", `{"high":"require_review","approval_rule":"orphan"}`)
		// Holds nothing.
		analyzerRule("no-hold", `{"high":"block"}`)
		// Its managed rule grants a force approver a level does not: the bare
		// hold stays, under that rule.
		forceRule("own-hold-forced", "{dba}")
		analyzerRule("own-hold-forced", `{"high":"require_review","approval_rule":"own-hold-forced"}`)
		// A review filed before the column existed.
		execSQL(t, `INSERT INTO private.reviews (id, org_id, connection_name, type, status, owner_id, owner_email, listener_name)
			VALUES (?, ?, '', 'onetime', 'PENDING', 'u', 'hoop@hoop.dev', 'old-lane')`, uuid.NewString(), testOrgID)
	})

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(approverVersion) })

	withDB(t, inst, func() {
		human := map[string]any{"action": "require_approval", "approver": "human", "approval_groups": []any{"dba", "sre"}}
		got := readSpec("own-hold")
		assert.Equal(t, human, got["high"])
		assert.Equal(t, human, got["medium"])
		assert.Equal(t, "warn", got["low"])
		assert.Equal(t, "return", got["review_mode"], "the rest of the spec stays")
		assert.NotContains(t, got, "approval_rule")
		assert.Equal(t, map[string]any{"high": "require_review", "approval_rule": "hand-made"}, readSpec("other-hold"))
		assert.Equal(t, map[string]any{"high": "require_review", "approval_rule": "orphan"}, readSpec("orphan"))
		assert.Equal(t, map[string]any{"high": "require_review", "approval_rule": "own-hold-forced"},
			readSpec("own-hold-forced"), "a rule with a force approver is not a level's policy")
		assert.Equal(t, map[string]any{"high": "block"}, readSpec("no-hold"))

		// An existing review reads as decided by a person.
		var approver string
		require.NoError(t, models.DB.Raw(`SELECT approver_type FROM private.reviews WHERE org_id = ? AND listener_name = 'old-lane'`,
			testOrgID).Row().Scan(&approver))
		assert.Equal(t, models.ApproverTypeHuman, approver)

		// An agent decision, as the plane files it, and object levels: a bare
		// one and an ai_agent hold.
		execSQL(t, `INSERT INTO private.reviews (id, org_id, connection_name, type, status, owner_id, owner_email,
			listener_name, approver_type, decision_reason, risk_level)
			VALUES (?, ?, '', 'onetime', 'APPROVED', 'u', 'hoop@hoop.dev', 'appdb', 'ai_agent', 'bounded', 'high')`,
			uuid.NewString(), testOrgID)
		execSQL(t, `UPDATE private.ai_session_analyzer_rules SET sidecar_spec = ?::jsonb WHERE org_id = ? AND name = 'no-hold'`,
			`{"high":{"action":"block"},"medium":{"action":"require_approval","approver":"ai_agent"}}`, testOrgID)

		// Human levels that name different groups: one rule cannot carry both.
		accessRule("split-groups", "ai-session-analyzer", "{dba}")
		analyzerRule("split-groups", splitGroups)
		// A bare hold under a hand-made rule beside a human level.
		accessRule("mixed-external", "ai-session-analyzer", "{dba}")
		analyzerRule("mixed-external", mixedExternal)
		// The same groups in another order: one approver set.
		accessRule("same-groups", "ai-session-analyzer", "{sre,dba}")
		analyzerRule("same-groups", `{"high":{"action":"require_approval","approver":"human","approval_groups":["dba","sre"]},`+
			`"medium":{"action":"require_approval","approver":"human","approval_groups":["sre"," dba"]}}`)
		// Groups the managed rule does not carry.
		accessRule("stale-rule", "ai-session-analyzer", "{dba}")
		analyzerRule("stale-rule", staleRule)
		accessRule("admin-level", "ai-session-analyzer", "{payments}")
		analyzerRule("admin-level", adminLevel)
		analyzerRule("implicit-agent", implicitAgent)
		accessRule("implicit-agent-mixed", "ai-session-analyzer", "{dba}")
		analyzerRule("implicit-agent-mixed", implicitAgentMixed)
		accessRule("explicit-agent-mixed", "ai-session-analyzer", "{dba}")
		analyzerRule("explicit-agent-mixed", explicitAgentMixed)
		// The managed rule names the level's groups and a force approver.
		forceRule("force-group", "{dba}")
		analyzerRule("force-group", forceGroup)
	})

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(beforeApproverVersion) })

	withDB(t, inst, func() {
		assert.Equal(t, map[string]any{"high": "require_review", "medium": "require_review", "low": "warn",
			"approval_rule": "own-hold", "review_mode": "return"}, readSpec("own-hold"))
		assert.Equal(t, map[string]any{"high": "require_review", "approval_rule": "hand-made"}, readSpec("other-hold"))
		// A bare object becomes its action; an ai_agent level has no older form.
		assert.Equal(t, map[string]any{"high": "block",
			"medium": map[string]any{"action": "require_approval", "approver": "ai_agent"}}, readSpec("no-hold"))
		// Specs whose bare form would change who approves stay objects.
		assert.JSONEq(t, splitGroups, specJSON(t, readSpec("split-groups")))
		assert.JSONEq(t, mixedExternal, specJSON(t, readSpec("mixed-external")))
		assert.JSONEq(t, staleRule, specJSON(t, readSpec("stale-rule")))
		assert.JSONEq(t, adminLevel, specJSON(t, readSpec("admin-level")),
			"an admin-only level must not fall to the managed rule's groups")
		assert.JSONEq(t, implicitAgent, specJSON(t, readSpec("implicit-agent")),
			"a hold object with no approver is the agent's, never a scalar hold")
		assert.JSONEq(t, implicitAgentMixed, specJSON(t, readSpec("implicit-agent-mixed")))
		assert.JSONEq(t, explicitAgentMixed, specJSON(t, readSpec("explicit-agent-mixed")))
		assert.JSONEq(t, forceGroup, specJSON(t, readSpec("force-group")),
			"a managed rule with a force approver does not carry the level's policy")
		assert.Equal(t, map[string]any{"high": "require_review", "medium": "require_review", "approval_rule": "same-groups"},
			readSpec("same-groups"))

		var status string
		require.NoError(t, models.DB.Raw(`SELECT status FROM private.reviews WHERE org_id = ? AND listener_name = 'appdb'`,
			testOrgID).Row().Scan(&status))
		assert.Equal(t, "EXECUTED", status, "an older binary must not release an agent approval")

		var columns int64
		require.NoError(t, models.DB.Raw(`SELECT count(*) FROM information_schema.columns
			WHERE table_schema = 'private' AND table_name = 'reviews'
			AND column_name IN ('approver_type', 'decision_reason', 'risk_level')`).Scan(&columns).Error)
		assert.Zero(t, columns)
	})

	// And up again: the down left a spec the up rewrites the same way.
	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(approverVersion) })
	withDB(t, inst, func() {
		human := map[string]any{"action": "require_approval", "approver": "human", "approval_groups": []any{"dba", "sre"}}
		assert.Equal(t, human, readSpec("own-hold")["high"])
	})
}
