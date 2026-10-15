package modelsbootstrap_test

import (
	"database/sql"
	"testing"

	"github.com/hoophq/hoop/gateway/pglite"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openPQ(t *testing.T, inst *pglite.Instance) *sql.DB {
	t.Helper()
	// lib/pq, not pgx: see hasLastLoginColumn.
	db, err := sql.Open("postgres", inst.DSN())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func hasAgentTagsTable(t *testing.T, inst *pglite.Instance) bool {
	t.Helper()
	db := openPQ(t, inst)
	defer db.Close()
	var found bool
	require.NoError(t, db.QueryRow(`SELECT to_regclass('private.agent_tags') IS NOT NULL`).Scan(&found))
	return found
}

// Migration 000143 rolls back: down drops private.agent_tags and keeps the
// agents, up creates the table again.
func TestAgentTagsMigrationRollsBack(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	require.True(t, hasAgentTagsTable(t, inst))

	const orgID = "00000000-0000-0000-0000-0000000000e1"
	const agentID = "00000000-0000-0000-0000-0000000000e2"
	db := openPQ(t, inst)
	_, err := db.Exec(`INSERT INTO private.orgs (id, name) VALUES ($1, 'agent-tags-migration')`, orgID)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO private.agents (id, org_id, name, mode, key_hash) VALUES ($1, $2, 'tagged', 'standard', 'hash')`, agentID, orgID)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO private.agent_tags (org_id, agent_id, key, value) VALUES ($1, $2, 'team', 'data')`, orgID, agentID)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	migrateTo(t, inst, 142)
	assert.False(t, hasAgentTagsTable(t, inst), "down must drop private.agent_tags")
	db = openPQ(t, inst)
	var agents int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM private.agents WHERE id = $1`, agentID).Scan(&agents))
	assert.Equal(t, 1, agents, "down must keep the agents")
	require.NoError(t, db.Close())

	migrateTo(t, inst, 143)
	assert.True(t, hasAgentTagsTable(t, inst), "up must create private.agent_tags again")
}
