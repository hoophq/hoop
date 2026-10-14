package models_test

import (
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The filters run against sessions written by UpsertSession, so the input
// predicate reads the blob shape the write path actually stores.
func TestListSessionsLiveAndInputFilters(t *testing.T) {
	startTestDB(t)

	now := time.Now().UTC()
	seed := func(id, verb, status, input string, age time.Duration) string {
		t.Helper()
		err := models.UpsertSession(models.Session{
			ID:             id,
			OrgID:          testOrgID,
			Connection:     "pg-prod",
			ConnectionType: "database",
			Verb:           verb,
			Status:         status,
			BlobInput:      models.BlobInputType(input),
			CreatedAt:      now.Add(-age),
		})
		require.NoError(t, err, "seed session %s", id)
		return id
	}
	liveOld := seed("00000000-0000-0000-0000-000000000b01", "connect", "open", "", 5*time.Minute)
	liveNew := seed("00000000-0000-0000-0000-000000000b02", "connect", "open", "", time.Minute)
	seed("00000000-0000-0000-0000-000000000b03", "connect", "done", "", 4*time.Minute)
	runningUpdate := seed("00000000-0000-0000-0000-000000000b04", "exec", "open",
		"UPDATE orders SET status = 'paid' WHERE id = 1", 3*time.Minute)
	doneDelete := seed("00000000-0000-0000-0000-000000000b05", "exec", "done",
		"delete from orders where id = 7", 2*time.Minute)
	doneSelect := seed("00000000-0000-0000-0000-000000000b06", "exec", "done",
		"SELECT * FROM orders", 90*time.Second)

	list := func(mutate func(*models.SessionOption)) ([]string, *int64) {
		t.Helper()
		opt := models.NewSessionOption()
		opt.CountMode = models.SessionCountExact
		mutate(&opt)
		got, err := models.ListSessions(testOrgID, "admin", true, opt)
		require.NoError(t, err)
		ids := make([]string, 0, len(got.Items))
		for _, s := range got.Items {
			ids = append(ids, s.ID)
		}
		return ids, got.Total
	}

	for _, tt := range []struct {
		msg    string
		mutate func(*models.SessionOption)
		want   []string
	}{
		{
			msg:    "live is an open connect session, the rule of the Live badge",
			mutate: func(o *models.SessionOption) { o.Status, o.Verb = "open", "connect" },
			want:   []string{liveNew, liveOld},
		},
		{
			msg:    "status alone also returns a running exec",
			mutate: func(o *models.SessionOption) { o.Status = "open" },
			want:   []string{liveNew, runningUpdate, liveOld},
		},
		{
			msg:    "input matches without case",
			mutate: func(o *models.SessionOption) { o.Input = "DELETE" },
			want:   []string{doneDelete},
		},
		{
			msg:    "a percent sign in the input is a wildcard",
			mutate: func(o *models.SessionOption) { o.Input = "update%orders" },
			want:   []string{runningUpdate},
		},
		{
			msg: "input composes with the other filters",
			mutate: func(o *models.SessionOption) {
				o.Input, o.Status, o.ConnectionName = "orders", "done", "pg-prod"
			},
			want: []string{doneSelect, doneDelete},
		},
		{
			msg:    "an input nothing holds matches nothing",
			mutate: func(o *models.SessionOption) { o.Input = "truncate" },
			want:   []string{},
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			ids, total := list(tt.mutate)
			assert.Equal(t, tt.want, ids)
			require.NotNil(t, total)
			assert.EqualValues(t, len(tt.want), *total, "the count must describe the same rows as the page")
		})
	}

	// The count is what the page shows as "N live sessions"; it must not need
	// the page to hold every live session.
	ids, total := list(func(o *models.SessionOption) { o.Status, o.Verb, o.Limit = "open", "connect", 1 })
	assert.Equal(t, []string{liveNew}, ids)
	require.NotNil(t, total)
	assert.EqualValues(t, 2, *total)
}

// The live filter must read index_sessions_open, or it walks every session the
// org ever had to find the few open ones. The gateway runs prepared
// statements, so the plan is checked as a generic plan: a status bound as a
// parameter would pass a custom plan check and still miss the index.
func TestLiveSessionFilterUsesOpenIndex(t *testing.T) {
	startTestDB(t)

	execSQL(t, `INSERT INTO private.sessions (org_id, connection, connection_type, verb, status, created_at)
		SELECT ?, 'pg', 'database', 'connect', 'done', now() - make_interval(secs => g)
		FROM generate_series(1, 5000) g`, testOrgID)
	execSQL(t, `INSERT INTO private.sessions (org_id, connection, connection_type, verb, status, created_at)
		SELECT ?, 'pg', 'database', 'connect', 'open', now() - make_interval(secs => g)
		FROM generate_series(1, 3) g`, testOrgID)
	execSQL(t, `ANALYZE private.sessions`)

	opt := models.NewSessionOption()
	opt.Status, opt.Verb = "open", "connect"
	from, where := models.SessionListFilterSQL(true, opt)
	execSQL(t, `PREPARE live_session_page(uuid) AS SELECT s.id`+from+` WHERE`+
		strings.ReplaceAll(where, "@org_id", "$1")+` ORDER BY s.created_at DESC, s.id DESC LIMIT 21`)
	execSQL(t, `SET plan_cache_mode = force_generic_plan`)
	t.Cleanup(func() {
		execSQL(t, `RESET plan_cache_mode`)
		execSQL(t, `DEALLOCATE live_session_page`)
	})

	rows, err := models.DB.Raw(`EXPLAIN EXECUTE live_session_page('` + testOrgID + `')`).Rows()
	require.NoError(t, err)
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan = append(plan, line)
	}
	require.NoError(t, rows.Err())
	assert.Contains(t, strings.Join(plan, "\n"), "index_sessions_open")
}
