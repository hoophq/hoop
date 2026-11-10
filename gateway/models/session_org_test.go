package models_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUpsertSessionKeepsOrg(t *testing.T) {
	startTestDB(t)
	const (
		otherOrgID = "00000000-0000-0000-0000-0000000000a2"
		sid        = "00000000-0000-0000-0000-000000001112"
	)
	require.NoError(t, models.DB.Exec(
		`INSERT INTO private.orgs (id, name) VALUES (?, 'other-org')`, otherOrgID).Error)

	session := func(orgID, connection string) models.Session {
		return models.Session{
			ID:             sid,
			OrgID:          orgID,
			Connection:     connection,
			ConnectionType: "database",
			Verb:           "exec",
			Status:         "open",
			BlobInput:      models.BlobInputType("SELECT 1"),
			CreatedAt:      time.Now().UTC(),
		}
	}
	require.NoError(t, models.UpsertSession(session(otherOrgID, "pg-b")))

	err := models.UpsertSession(session(testOrgID, "pg-a"))
	require.ErrorIs(t, err, models.ErrSessionOfAnotherOrg, "an upsert from another org must not take the session")

	var row struct{ OrgID, Connection string }
	require.NoError(t, models.DB.Raw(
		`SELECT org_id, connection FROM private.sessions WHERE id = ?`, sid).Scan(&row).Error)
	assert.Equal(t, otherOrgID, row.OrgID)
	assert.Equal(t, "pg-b", row.Connection)

	var blobs int64
	require.NoError(t, models.DB.Raw(
		`SELECT count(*) FROM private.blobs WHERE org_id = ?`, testOrgID).Scan(&blobs).Error)
	assert.Zero(t, blobs, "the refused upsert must roll back its input blob")

	update := session(otherOrgID, "pg-b")
	update.Status = "done"
	require.NoError(t, models.UpsertSession(update), "the owner org still updates its session")
	var status string
	require.NoError(t, models.DB.Raw(
		`SELECT status FROM private.sessions WHERE id = ?`, sid).Scan(&status).Error)
	assert.Equal(t, "done", status)
}

func TestGetSessionOrgID(t *testing.T) {
	startTestDB(t)
	const sid = "00000000-0000-0000-0000-000000001113"
	require.NoError(t, models.DB.Exec(`INSERT INTO private.sessions (id, org_id, connection, connection_type, verb, status)
		VALUES (?, ?, 'pg', 'database', 'exec', 'open')`, sid, testOrgID).Error)

	for _, id := range uuidSpellings(sid) {
		orgID, err := models.GetSessionOrgID(models.DB, id)
		require.NoError(t, err, "id %q", id)
		assert.Equal(t, testOrgID, orgID, "id %q", id)
	}

	for _, id := range []string{"00000000-0000-0000-0000-000000001114", "not-a-uuid"} {
		_, err := models.GetSessionOrgID(models.DB, id)
		assert.True(t, errors.Is(err, gorm.ErrRecordNotFound), "id %q: %v", id, err)
	}
}

// On a single-org gateway every session shares the org: an upsert must
// still write only its own row.
func TestUpsertSessionWritesOnlyItsRow(t *testing.T) {
	startTestDB(t)
	ids := []string{"00000000-0000-0000-0000-000000001115", "00000000-0000-0000-0000-000000001116"}
	for _, id := range ids {
		require.NoError(t, models.UpsertSession(models.Session{
			ID: id, OrgID: testOrgID, Connection: "pg", ConnectionType: "database",
			Verb: "exec", Status: "open", BlobInput: models.BlobInputType("SELECT " + id), CreatedAt: time.Now().UTC(),
		}))
	}
	require.NoError(t, models.UpsertSession(models.Session{
		ID: ids[0], OrgID: testOrgID, Connection: "pg", ConnectionType: "database",
		Verb: "exec", Status: "done", BlobInput: models.BlobInputType("SELECT 1"), CreatedAt: time.Now().UTC(),
	}))

	var rows []struct{ ID, Status string }
	require.NoError(t, models.DB.Raw(`SELECT id::TEXT, status FROM private.sessions WHERE org_id = ? ORDER BY id`,
		testOrgID).Scan(&rows).Error)
	require.Len(t, rows, 2)
	assert.Equal(t, "done", rows[0].Status)
	assert.Equal(t, "open", rows[1].Status)
}

func TestGetAgentOrgID(t *testing.T) {
	startTestDB(t)
	require.NoError(t, models.CreateAgent(testOrgID, "_default", "standard", "hash-a"))
	agent, err := models.GetAgentByNameOrID(testOrgID, "_default")
	require.NoError(t, err)

	for _, id := range uuidSpellings(agent.ID) {
		orgID, err := models.GetAgentOrgID(models.DB, id)
		require.NoError(t, err, "id %q", id)
		assert.Equal(t, testOrgID, orgID, "id %q", id)
	}
	for _, id := range []string{"00000000-0000-0000-0000-0000000011ff", "not-a-uuid", ""} {
		_, err := models.GetAgentOrgID(models.DB, id)
		assert.True(t, errors.Is(err, gorm.ErrRecordNotFound), "id %q: %v", id, err)
	}
}

// uuidSpellings returns id in the spellings Postgres reads as the same uuid.
func uuidSpellings(id string) []string {
	hex := strings.ReplaceAll(id, "-", "")
	var groups []string
	for i := 0; i < len(hex); i += 4 {
		groups = append(groups, hex[i:i+4])
	}
	return []string{id, strings.ToUpper(id), hex, "{" + id + "}", strings.Join(groups, "-")}
}
