package apisidecar

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apisession "github.com/hoophq/hoop/gateway/api/session"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A real sidecar, in front of a real Postgres, sends its sessions to the real
// events handler.
//
// Skipped unless both are set:
//
//	HOOP_SIDECAR_E2E_BIN       a hoop binary built from this branch
//	HOOP_SIDECAR_E2E_UPSTREAM  a Postgres URL the sidecar fronts, e.g.
//	                           postgres://postgres@127.0.0.1:5432/postgres
//
// The gateway side is the handlers on a test router: the license middleware
// needs a signed Enterprise license this repository cannot make.
func TestE2ESessionEventsAgainstARealSidecar(t *testing.T) {
	bin := os.Getenv("HOOP_SIDECAR_E2E_BIN")
	upstream := os.Getenv("HOOP_SIDECAR_E2E_UPSTREAM")
	if bin == "" || upstream == "" {
		t.Skip("set HOOP_SIDECAR_E2E_BIN and HOOP_SIDECAR_E2E_UPSTREAM to run against a real sidecar")
	}
	up, err := url.Parse(upstream)
	require.NoError(t, err)

	startSwitchDB(t)
	featureflag.Set(switchOrgID, services.SidecarSessionEventsFlag, true)
	t.Cleanup(func() { featureflag.SetAll(switchOrgID, map[string]bool{}) })

	var eventsDown atomic.Bool
	srv := eventsE2EServer(t, &eventsDown)

	const listenPort, adminPort = 15461, 15462
	sc, token := newRow(t, "events-e2e")
	doc := fmt.Sprintf(`{"listeners":[{"name":"appdb","protocol":"postgres",`+
		`"listen":"127.0.0.1:%d","upstream":%q,`+
		`"guardrails":{"mode":"enforce","rules":[{"name":"no-drop","type":"deny_words_list","words":["drop table"]}]}}],`+
		`"admin":{"listen":"127.0.0.1:%d"}}`, listenPort, up.Host, adminPort)
	w, _ := putConfig(t, sc.ID, doc)
	require.Equal(t, http.StatusOK, w.Code, "put: %s", w.Body)

	p := startSidecar(t, bin, srv.URL, token, filepath.Join(t.TempDir(), "sidecar.log"))
	waitRow(t, sc.ID, 30*time.Second, "the boot handshake", func(r openapi.SidecarResponse) bool {
		return r.LastSeenAt != nil
	})
	stored, err := models.GetSidecarByNameOrID(models.DB, switchOrgID, sc.ID)
	require.NoError(t, err)
	assert.Contains(t, []string(stored.Capabilities), "session_events", "the sidecar names the capability")
	waitLog(t, p, "the control plane takes session events")

	clientURL := *up
	clientURL.Host = fmt.Sprintf("127.0.0.1:%d", listenPort)
	q := clientURL.Query()
	q.Set("sslmode", "disable")
	clientURL.RawQuery = q.Encode()
	mirror := sc.Name + "-appdb"

	t.Run("a session reaches the session list", func(t *testing.T) {
		runSession(t, clientURL.String(), []string{"SELECT 1", "SELECT 'sidecar-e2e'"}, "DROP TABLE nope")

		sess := waitSidecarSession(t, mirror, 1, 30*time.Second)
		assert.Equal(t, "database", sess.ConnectionType)
		assert.Equal(t, "postgres", sess.ConnectionSubtype)
		assert.Equal(t, "connect", sess.Verb)
		assert.Equal(t, "raw", *sess.RecordingFormat)
		assert.Equal(t, "sidecar", sess.IdentityType)
		assert.Equal(t, "sidecar", sess.Origin)
		assert.Equal(t, up.User.Username(), sess.UserName, "the database role is the principal")
		require.Len(t, sess.GuardRailsInfo, 1)
		assert.Equal(t, "no-drop", sess.GuardRailsInfo[0].RuleName)
		assert.NotNil(t, sess.EndSession, "session_end closed it")

		// What the session page asks for a done Postgres session.
		inputs, errs := viewerStream(t, sess.ID)
		// Once: the server's CommandComplete tag also reads "SELECT 1", and
		// it is output, not a second query.
		assert.Equal(t, 1, countOf(inputs, "SELECT 1"), "inputs: %q", inputs)
		assert.Contains(t, inputs, "SELECT 'sidecar-e2e'")
		assert.Contains(t, inputs, "DROP TABLE nope", "a denied statement stays in the query list")
		require.NotEmpty(t, errs)
		assert.Contains(t, errs[0], `denied by rule "no-drop"`)

		stats := sidecarStats(t, adminPort)
		assert.Equal(t, true, stats["enabled"])
		assert.EqualValues(t, 0, stats["dropped"])
		assert.Greater(t, stats["sent"].(float64), float64(0))
	})

	t.Run("a gateway outage neither blocks statements nor loses them", func(t *testing.T) {
		eventsDown.Store(true)
		start := time.Now()
		runSession(t, clientURL.String(), []string{"SELECT 'during the outage'"}, "")
		assert.Less(t, time.Since(start), 5*time.Second, "the statement waited for the gateway")

		time.Sleep(3 * time.Second)
		assert.Equal(t, 1, countSidecarSessions(t, mirror), "nothing lands while the gateway answers 503")
		waitLog(t, p, "resending later")

		eventsDown.Store(false)
		waitSidecarSession(t, mirror, 2, 90*time.Second)
		found := false
		for _, s := range listSidecarSessions(t, mirror) {
			inputs, _ := viewerStream(t, s.ID)
			for _, in := range inputs {
				found = found || in == "SELECT 'during the outage'"
			}
		}
		assert.True(t, found, "the resent session holds its statement")
	})

	t.Run("the flag going off stops the sidecar at once", func(t *testing.T) {
		featureflag.Set(switchOrgID, services.SidecarSessionEventsFlag, false)
		runSession(t, clientURL.String(), []string{"SELECT 'flag off'"}, "")
		waitLog(t, p, "refused session events")
		assert.Equal(t, 2, countSidecarSessions(t, mirror))
		stats := sidecarStats(t, adminPort)
		assert.Equal(t, false, stats["enabled"])
		assert.Greater(t, stats["rejected"].(float64), float64(0))
	})

	// The local audit file is the record of truth, and holds every statement
	// whatever the gateway did.
	for _, stmt := range []string{"SELECT 'during the outage'", "SELECT 'flag off'"} {
		assert.Contains(t, p.logText(t), stmt)
	}
	if t.Failed() {
		t.Logf("sidecar log:\n%s", p.logText(t))
	}
}

// eventsE2EServer is handshakeServer with the events route. down makes the
// route answer 503, the way a gateway in trouble does.
func eventsE2EServer(t *testing.T, down *atomic.Bool) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var base string
	auth := func(c *gin.Context) {
		sc, err := models.GetSidecarByKeyHash(models.DB, models.HashAPIKey(c.GetHeader("hoop-sidecar-token")))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
			return
		}
		c.Set("sidecar-auth", sc)
		c.Set(storagev2.ContextKey, storagev2.NewOrganizationContext(sc.OrgID).WithApiURL(base))
		c.Next()
	}
	outage := func(c *gin.Context) {
		if down.Load() {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"message": "down for the test"})
			return
		}
		c.Next()
	}
	r.POST("/api/sidecars/handshake", auth, Handshake)
	r.GET("/api/sidecars/configuration", auth, Configuration)
	r.PUT("/api/sidecars/configuration", auth, ImportConfiguration)
	r.POST("/api/sidecars/events", outage, auth, PostEvents)
	srv := httptest.NewServer(r)
	base = srv.URL
	t.Cleanup(srv.Close)
	return srv
}

// runSession opens one connection through the sidecar, runs the statements,
// expects denied to be refused, and closes it.
func runSession(t *testing.T, dsn string, statements []string, denied string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	defer db.Close()
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	for _, s := range statements {
		var v any
		require.NoError(t, conn.QueryRowContext(ctx, s).Scan(&v), "statement %q", s)
	}
	if denied != "" {
		_, err := conn.ExecContext(ctx, denied)
		require.Error(t, err, "the sidecar let %q through", denied)
	}
	require.NoError(t, conn.Close())
}

func listSidecarSessions(t *testing.T, connection string) []models.Session {
	t.Helper()
	var ids []string
	require.NoError(t, models.DB.Raw(`SELECT id FROM private.sessions
		WHERE org_id = ? AND origin = 'sidecar' AND connection = ? ORDER BY created_at`,
		switchOrgID, connection).Scan(&ids).Error)
	var out []models.Session
	for _, id := range ids {
		s, err := models.GetSessionByID(switchOrgID, id)
		require.NoError(t, err)
		out = append(out, *s)
	}
	return out
}

func countSidecarSessions(t *testing.T, connection string) int {
	return len(listSidecarSessions(t, connection))
}

// waitSidecarSession waits for n done sessions and returns the latest.
func waitSidecarSession(t *testing.T, connection string, n int, timeout time.Duration) models.Session {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		all := listSidecarSessions(t, connection)
		done := 0
		for _, s := range all {
			if s.Status == "done" {
				done++
			}
		}
		if done >= n {
			return all[len(all)-1]
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d done sidecar sessions; have %d sessions, %d done", n, len(all), done)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// viewerStream calls GET /api/sessions/:id the way the session page does for
// a done Postgres session, and returns the decoded "i" and "e" entries.
func viewerStream(t *testing.T, sessionID string) (inputs, errs []string) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet,
		"/api/sessions/"+sessionID+"?expand=event_stream&event_stream=raw-queries", nil)
	c.Params = gin.Params{{Key: "session_id", Value: sessionID}}
	c.Set(storagev2.ContextKey, storagev2.NewContext("admin-1", switchOrgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", []string{types.GroupAdmin}))
	apisession.Get(c)
	require.Equal(t, http.StatusOK, w.Code, "session get: %s", w.Body)
	var resp struct {
		EventStream []json.RawMessage `json:"event_stream"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	for _, raw := range resp.EventStream {
		var entry []any
		require.NoError(t, json.Unmarshal(raw, &entry))
		require.Len(t, entry, 3)
		text, err := base64.StdEncoding.DecodeString(entry[2].(string))
		require.NoError(t, err)
		switch entry[1] {
		case "i":
			inputs = append(inputs, string(text))
		case "e":
			errs = append(errs, string(text))
		}
	}
	return inputs, errs
}

func sidecarStats(t *testing.T, port int) map[string]any {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/stats", port))
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var out struct {
		SessionEvents map[string]any `json:"session_events"`
	}
	require.NoError(t, json.Unmarshal(body, &out), "stats: %s", body)
	require.NotNil(t, out.SessionEvents, "stats carry no session_events: %s", body)
	return out.SessionEvents
}

func waitLog(t *testing.T, p *sidecarProc, line string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(p.logText(t), line) {
		if time.Now().After(deadline) {
			t.Fatalf("the sidecar never logged %q:\n%s", line, p.logText(t))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func countOf(list []string, v string) int {
	n := 0
	for _, s := range list {
		if s == v {
			n++
		}
	}
	return n
}
