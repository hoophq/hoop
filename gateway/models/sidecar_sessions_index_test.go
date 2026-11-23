package models_test

import (
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sidecarIDTextIndex = "idx_sidecars_id_text"

// The reaper finds each open session's sidecar through an index. Without it,
// every open session reads every sidecar of its org: 5.6 s a pass with 28,000
// open sessions and 3,000 sidecars.
func TestTheStaleSidecarSessionListFindsTheSidecarByIndex(t *testing.T) {
	startTestDB(t)
	// Enough rows that the planner does not scan the table whatever the index.
	execSQL(t, `INSERT INTO private.sidecars (org_id, name, created_by, last_seen_at)
		SELECT ?, 'reaper-' || n, 'tests@hoop.dev', now() FROM generate_series(1, 5000) n`, testOrgID)
	execSQL(t, `INSERT INTO private.sessions (org_id, connection, connection_type, verb, status, origin, metadata)
		SELECT sc.org_id, 'reaper-pg', 'database', 'connect', 'open', ?,
			jsonb_build_object('sidecar', jsonb_build_object('id', sc.id))
		FROM private.sidecars sc WHERE sc.org_id = ?`, proto.SessionOriginSidecar, testOrgID)
	execSQL(t, `ANALYZE private.sidecars`)
	execSQL(t, `ANALYZE private.sessions`)

	plan := func(t *testing.T) string {
		t.Helper()
		now := time.Now().UTC()
		rows, err := models.DB.Raw(`EXPLAIN `+models.StaleSidecarSessionsQuery, map[string]any{
			"origin": proto.SessionOriginSidecar,
			"seen":   now.Add(-5 * time.Minute),
			"event":  now.Add(-24 * time.Hour),
			"limit":  100,
		}).Rows()
		require.NoError(t, err)
		defer rows.Close()
		var lines []string
		for rows.Next() {
			var line string
			require.NoError(t, rows.Scan(&line))
			lines = append(lines, line)
		}
		require.NoError(t, rows.Err())
		return strings.Join(lines, "\n")
	}

	// Each session probes its sidecar by id; idx_sidecars_org_name would read
	// every sidecar of the org.
	const probe = "Index Cond: ((id)::text ="
	got := plan(t)
	assert.Contains(t, got, sidecarIDTextIndex)
	assert.Contains(t, got, probe)

	t.Run("without the index", func(t *testing.T) {
		execSQL(t, `DROP INDEX private.`+sidecarIDTextIndex)
		got := plan(t)
		assert.NotContains(t, got, sidecarIDTextIndex)
		assert.NotContains(t, got, probe)
	})
}
