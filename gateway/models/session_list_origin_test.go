package models_test

import (
	"testing"
	"time"

	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The list and the details page read origin from the same column. A session
// recorded before the column existed has NULL there and reads as "".
func TestListSessionsReturnsOrigin(t *testing.T) {
	startTestDB(t)

	now := time.Now().UTC()
	want := map[string]string{
		"00000000-0000-0000-0000-000000000c01": proto.SessionOriginCLI,
		"00000000-0000-0000-0000-000000000c02": proto.SessionOriginWebApp,
		"00000000-0000-0000-0000-000000000c03": "",
	}
	i := 0
	for id, origin := range want {
		i++
		require.NoError(t, models.UpsertSession(models.Session{
			ID:             id,
			OrgID:          testOrgID,
			Connection:     "pg-prod",
			ConnectionType: "database",
			Verb:           "exec",
			Status:         "done",
			Origin:         origin,
			CreatedAt:      now.Add(-time.Duration(i) * time.Minute),
		}))
	}
	require.NoError(t, models.DB.Exec(
		`UPDATE private.sessions SET origin = NULL WHERE id = ?`,
		"00000000-0000-0000-0000-000000000c03").Error)

	opt := models.NewSessionOption()
	got, err := models.ListSessions(testOrgID, "admin", true, opt)
	require.NoError(t, err)
	gotOrigins := map[string]string{}
	for _, s := range got.Items {
		gotOrigins[s.ID] = s.Origin
	}
	assert.Equal(t, want, gotOrigins)

	for id, origin := range want {
		sess, err := models.GetSessionByID(testOrgID, id)
		require.NoError(t, err)
		assert.Equal(t, origin, sess.Origin, "session %s", id)
	}
}
