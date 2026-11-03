package models_test

import (
	"testing"

	"github.com/hoophq/hoop/gateway/migrations"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReviewNotificationsAreOffOnlyForAgentOrgsWithoutASidecar(t *testing.T) {
	startTestDB(t)
	const (
		agentOnly       = "00000000-0000-0000-0000-0000000001a1"
		agentAndSidecar = "00000000-0000-0000-0000-0000000001a2"
		sidecarOnly     = "00000000-0000-0000-0000-0000000001a3"
		adminChose      = "00000000-0000-0000-0000-0000000001a4"
	)
	exec := func(query string, args ...any) {
		t.Helper()
		require.NoError(t, models.DB.Exec(query, args...).Error)
	}
	for _, id := range []string{agentOnly, agentAndSidecar, sidecarOnly, adminChose} {
		exec(`INSERT INTO private.orgs (id, name) VALUES (?, ?)`, id, "org-"+id[len(id)-2:])
	}
	for _, id := range []string{agentOnly, agentAndSidecar, adminChose} {
		exec(`INSERT INTO private.org_feature_flags (org_id, name, enabled, updated_by)
			VALUES (?, 'experimental.agents', true, 'migration')`, id)
	}
	for _, id := range []string{agentAndSidecar, sidecarOnly} {
		require.NoError(t, models.CreateSidecar(models.DB, &models.Sidecar{
			OrgID: id, Name: "sc", KeyHash: models.HashAPIKey("hsc_" + id), CreatedBy: "tests@hoop.dev"}))
	}
	exec(`INSERT INTO private.org_feature_flags (org_id, name, enabled, updated_by)
		VALUES (?, 'experimental.review_notifications', true, 'admin@hoop.dev')`, adminChose)

	run := func(file string) {
		t.Helper()
		sql, err := migrations.FS.ReadFile(file)
		require.NoError(t, err)
		exec(string(sql))
	}
	stored := func() map[string]bool {
		t.Helper()
		var rows []struct {
			OrgID   string
			Enabled bool
		}
		require.NoError(t, models.DB.Raw(`SELECT org_id::text AS org_id, enabled FROM private.org_feature_flags
			WHERE name = 'experimental.review_notifications' AND org_id IN (?, ?, ?, ?)`,
			agentOnly, agentAndSidecar, sidecarOnly, adminChose).Scan(&rows).Error)
		got := map[string]bool{}
		for _, r := range rows {
			got[r.OrgID] = r.Enabled
		}
		return got
	}

	run("000145_review_notifications_agent_orgs.up.sql")
	assert.Equal(t, map[string]bool{agentOnly: false, adminChose: true}, stored(),
		"only the agent org without a sidecar is turned off; the admin's choice stays")

	run("000145_review_notifications_agent_orgs.down.sql")
	assert.Equal(t, map[string]bool{adminChose: true}, stored(), "the rollback removes only the migration's rows")
}
