package models_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A refused exec leaves the row POST /sessions wrote. Only that row closes: a
// refusal must not end another user's session, a session on another
// connection, one that is not open, or one that already streamed.
func TestCloseRefusedSession(t *testing.T) {
	startTestDB(t)

	now := time.Now().UTC()
	seed := func(id, userID, connection, status string) {
		t.Helper()
		require.NoError(t, models.UpsertSession(models.Session{
			ID:             id,
			OrgID:          testOrgID,
			UserID:         userID,
			Connection:     connection,
			ConnectionType: "database",
			Verb:           "exec",
			Status:         status,
			CreatedAt:      now.Add(-time.Minute),
		}))
	}
	type row struct {
		Status   string
		ExitCode *int
		EndedAt  *time.Time
	}
	read := func(id string) row {
		t.Helper()
		var r row
		require.NoError(t, models.DB.Raw(`SELECT status, exit_code, ended_at
			FROM private.sessions WHERE org_id = ? AND id = ?`, testOrgID, id).Scan(&r).Error)
		return r
	}
	closeIt := func(userID, connection, id string) bool {
		t.Helper()
		closed, err := models.CloseRefusedSession(models.DB, testOrgID, userID, connection, id,
			254, "exec is disabled for this connection", now)
		require.NoError(t, err)
		return closed
	}

	const refused = "00000000-0000-0000-0000-000000000c01"
	seed(refused, "alice", "pg-off", "open")
	assert.True(t, closeIt("alice", "pg-off", refused))
	got := read(refused)
	assert.Equal(t, "done", got.Status)
	require.NotNil(t, got.ExitCode)
	assert.Equal(t, 254, *got.ExitCode)
	require.NotNil(t, got.EndedAt)

	// The reason is the session output, so the Sessions page says why.
	sess, err := models.GetSessionByID(testOrgID, refused)
	require.NoError(t, err)
	blob, err := sess.GetBlobStream()
	require.NoError(t, err)
	require.NotNil(t, blob)
	var events [][]any
	require.NoError(t, json.Unmarshal(blob.BlobStream, &events))
	require.Len(t, events, 1)
	assert.Equal(t, "e", events[0][1])
	out, err := base64.StdEncoding.DecodeString(events[0][2].(string))
	require.NoError(t, err)
	assert.Equal(t, "exec is disabled for this connection", string(out))

	assert.False(t, closeIt("alice", "pg-off", refused), "a closed session closes once")

	for _, tt := range []struct {
		msg                    string
		id, owner, conn, state string
		asUser, asConn         string
		streamed               bool
	}{
		{"another user's session", "00000000-0000-0000-0000-000000000c02", "bob", "pg-off", "open", "alice", "pg-off", false},
		{"a session on another connection", "00000000-0000-0000-0000-000000000c03", "alice", "pg-prod", "open", "alice", "pg-off", false},
		{"a reviewed session waiting to run", "00000000-0000-0000-0000-000000000c04", "alice", "pg-off", "ready", "alice", "pg-off", false},
		{"a session that streamed", "00000000-0000-0000-0000-000000000c05", "alice", "pg-off", "open", "alice", "pg-off", true},
	} {
		seed(tt.id, tt.owner, tt.conn, tt.state)
		if tt.streamed {
			// The audit plugin creates the stream blob when a session starts.
			require.NoError(t, models.CreateEmptySessionStreamBlob(testOrgID, tt.id, nil))
		}
		assert.False(t, closeIt(tt.asUser, tt.asConn, tt.id), tt.msg)
		assert.Equal(t, tt.state, read(tt.id).Status, tt.msg)
		assert.Nil(t, read(tt.id).EndedAt, tt.msg)
	}
}
