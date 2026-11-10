package models_test

import (
	"errors"
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
	require.Error(t, err, "an upsert from another org must not take the session")

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

	orgID, err := models.GetSessionOrgID(models.DB, sid)
	require.NoError(t, err)
	assert.Equal(t, testOrgID, orgID)

	for _, id := range []string{"00000000-0000-0000-0000-000000001114", "not-a-uuid"} {
		_, err := models.GetSessionOrgID(models.DB, id)
		assert.True(t, errors.Is(err, gorm.ErrRecordNotFound), "id %q: %v", id, err)
	}
}

func TestGetAgentByIDIsOrgScoped(t *testing.T) {
	startTestDB(t)
	const otherOrgID = "00000000-0000-0000-0000-0000000000a3"
	require.NoError(t, models.DB.Exec(
		`INSERT INTO private.orgs (id, name) VALUES (?, 'other-org')`, otherOrgID).Error)
	require.NoError(t, models.CreateAgent(otherOrgID, "_default", "standard", "hash-b"))
	other, err := models.GetAgentByNameOrID(otherOrgID, "_default")
	require.NoError(t, err)

	_, err = models.GetAgentByID(models.DB, testOrgID, other.ID)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound), "agent of another org: %v", err)
	_, err = models.GetAgentByID(models.DB, testOrgID, "not-a-uuid")
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound), "invalid id: %v", err)

	got, err := models.GetAgentByID(models.DB, otherOrgID, other.ID)
	require.NoError(t, err)
	assert.Equal(t, "_default", got.Name)
}
