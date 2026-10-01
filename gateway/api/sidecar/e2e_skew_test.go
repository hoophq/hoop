package apisidecar

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Real sidecar binaries against the real handshake handler (ADR-0022).
//
// Skipped unless HOOP_SIDECAR_E2E_BINS names the binaries to drive, as
// label=path pairs: "new=/tmp/hoop-new,1.197.0=/tmp/hoop-1.197.0". A label
// starting with "new" is a build of this branch; every other label is a
// release that predates it. The heartbeat is one minute in a released build,
// so each binary takes several minutes.
func TestE2ESkewAgainstRealSidecars(t *testing.T) {
	spec := os.Getenv("HOOP_SIDECAR_E2E_BINS")
	if spec == "" {
		t.Skip("set HOOP_SIDECAR_E2E_BINS=label=path,... to run against real sidecar binaries")
	}
	startSwitchDB(t)
	srv := handshakeServer(t)

	t.Run("save gate reads the header on the row", func(t *testing.T) { saveGate(t, srv) })
	t.Run("deprecated keys are listed", func(t *testing.T) { deprecations(t) })

	for i, pair := range strings.Split(spec, ",") {
		label, bin, ok := strings.Cut(strings.TrimSpace(pair), "=")
		require.True(t, ok, "HOOP_SIDECAR_E2E_BINS entry %q is not label=path", pair)
		port := 15441 + i
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			driveBinary(t, srv, label, bin, port)
		})
	}
}

// handshakeServer is the control plane as a sidecar sees it: the token
// middleware and the three sidecar routes, on a random port.
func handshakeServer(t *testing.T) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var url string
	auth := func(c *gin.Context) {
		sc, err := models.GetSidecarByKeyHash(models.DB, models.HashAPIKey(c.GetHeader("hoop-sidecar-token")))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
			return
		}
		c.Set("sidecar-auth", sc)
		c.Set(storagev2.ContextKey, storagev2.NewOrganizationContext(sc.OrgID).WithApiURL(url))
		c.Next()
	}
	r.POST("/api/sidecars/handshake", auth, Handshake)
	r.GET("/api/sidecars/configuration", auth, Configuration)
	r.PUT("/api/sidecars/configuration", auth, ImportConfiguration)
	srv := httptest.NewServer(r)
	url = srv.URL
	t.Cleanup(srv.Close)
	return srv
}

func listenerDoc(port int, extra string) string {
	return fmt.Sprintf(`{"listeners":[{"name":"appdb","protocol":"postgres","listen":"127.0.0.1:%d","upstream":"127.0.0.1:5432"}]%s}`, port, extra)
}

func newRow(t *testing.T, name string) (*models.Sidecar, string) {
	t.Helper()
	token := "hsc_e2e_" + name
	sc := &models.Sidecar{OrgID: switchOrgID, Name: name, KeyHash: models.HashAPIKey(token), CreatedBy: "e2e@hoop.dev"}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	return sc, token
}

func getRow(t *testing.T, id string) openapi.SidecarResponse {
	t.Helper()
	w, resp := callAdmin(t, Get, http.MethodGet, id, `{}`)
	require.Equal(t, http.StatusOK, w.Code, "get: %s", w.Body)
	return resp
}

func putConfig(t *testing.T, id, doc string) (*httptest.ResponseRecorder, openapi.SidecarResponse) {
	t.Helper()
	return callAdmin(t, Put, http.MethodPut, id, doc)
}

// handshakeAs is what a build with the given header would send, without
// running one.
func handshakeAs(t *testing.T, srv *httptest.Server, token, header, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/sidecars/handshake", bytes.NewReader([]byte(body)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("hoop-sidecar-token", token)
	if header != "" {
		req.Header.Set("hoop-sidecar-capabilities", header)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func waitRow(t *testing.T, id string, timeout time.Duration, what string, ok func(openapi.SidecarResponse) bool) openapi.SidecarResponse {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		row := getRow(t, id)
		if ok(row) {
			return row
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; row: state=%q outcome=%q served=%q applied=%q error=%q",
				what, row.ConfigState, row.LastOutcome, row.ServedRevision, row.AppliedRevision, row.LastError)
		}
		time.Sleep(2 * time.Second)
	}
}

func saveGate(t *testing.T, srv *httptest.Server) {
	sc, token := newRow(t, "gate")
	w, _ := putConfig(t, sc.ID, listenerDoc(15400, ""))
	require.Equal(t, http.StatusOK, w.Code, "put: %s", w.Body)

	// A build that lists rule types and protocols, but speaks mysql only: the
	// serve refuses the postgres listener, and records the header so the
	// next save is refused too.
	resp := handshakeAs(t, srv, token, "review_mode,rule:operation,protocol:mysql", `{"version":"1.197.0"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, "the serve gate")
	w, _ = putConfig(t, sc.ID, listenerDoc(15400, ""))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "put: %s", w.Body)
	assert.Contains(t, w.Body.String(), `listener \"appdb\" speaks postgres`)
	assert.Contains(t, w.Body.String(), `protocol:postgres`)

	// The same build, now reporting postgres.
	handshakeAs(t, srv, token, "review_mode,rule:operation,protocol:postgres", `{"version":"1.197.0"}`)
	w, _ = putConfig(t, sc.ID, listenerDoc(15400, ""))
	assert.Equal(t, http.StatusOK, w.Code, "put: %s", w.Body)

	// A header from before the generated list gets the frozen baseline.
	handshakeAs(t, srv, token, "review_mode", `{"version":"1.197.0"}`)
	w, _ = putConfig(t, sc.ID, listenerDoc(15400, `,"guardrails":{"rules":[{"name":"h","type":"http_header","headers":{"x-a":["1"]}}]}`))
	assert.Equal(t, http.StatusOK, w.Code, "baseline vocabulary refused: %s", w.Body)
}

func deprecations(t *testing.T) {
	sc, _ := newRow(t, "deprecated")
	w, resp := putConfig(t, sc.ID, listenerDoc(15401, `,"audit":{"fail_closed":true}`))
	require.Equal(t, http.StatusOK, w.Code, "put: %s", w.Body)
	require.NotEmpty(t, resp.Deprecations, "the deprecated spelling is not listed")
	assert.Contains(t, resp.Deprecations[0], "audit.fail_closed")
	assert.Contains(t, getRow(t, sc.ID).Deprecations[0], "audit.fail_closed")
}

type sidecarProc struct {
	cmd  *exec.Cmd
	done chan error
	log  string
}

func startSidecar(t *testing.T, bin, planeURL, token, logPath string) *sidecarProc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "start", "sidecar")
	cmd.Env = append(os.Environ(),
		"HOOP_CONTROL_PLANE_URL="+planeURL,
		"HOOP_SIDECAR_TOKEN="+token,
		"HOME="+filepath.Dir(logPath),
	)
	f, err := os.Create(logPath)
	require.NoError(t, err)
	cmd.Stdout, cmd.Stderr = f, f
	require.NoError(t, cmd.Start())
	p := &sidecarProc{cmd: cmd, done: make(chan error, 1), log: logPath}
	go func() { p.done <- cmd.Wait(); f.Close() }()
	t.Cleanup(func() { cancel(); <-p.done })
	return p
}

func (p *sidecarProc) exited(within time.Duration) (bool, error) {
	select {
	case err := <-p.done:
		p.done <- err
		return true, err
	case <-time.After(within):
		return false, nil
	}
}

func (p *sidecarProc) logText(t *testing.T) string {
	b, err := os.ReadFile(p.log)
	require.NoError(t, err)
	return string(b)
}

func driveBinary(t *testing.T, srv *httptest.Server, label, bin string, port int) {
	isNew := strings.HasPrefix(label, "new")
	sc, token := newRow(t, "bin-"+strings.ReplaceAll(label, ".", "-"))
	w, _ := putConfig(t, sc.ID, listenerDoc(port, ""))
	require.Equal(t, http.StatusOK, w.Code, "put: %s", w.Body)
	dir := t.TempDir()

	// Boot: the served document decodes on this build, and the row records
	// the handshake and its header.
	p := startSidecar(t, bin, srv.URL, token, filepath.Join(dir, "boot.log"))
	row := waitRow(t, sc.ID, 30*time.Second, "the boot handshake", func(r openapi.SidecarResponse) bool {
		return r.LastSeenAt != nil && r.ServedRevision != ""
	})
	if exited, err := p.exited(5 * time.Second); exited {
		t.Fatalf("%s exited at boot (%v):\n%s", label, err, p.logText(t))
	}
	assert.Equal(t, "", row.LastOutcome, "a boot handshake reports no outcome")
	stored, err := models.GetSidecarByNameOrID(models.DB, switchOrgID, sc.ID)
	require.NoError(t, err)
	caps := []string(stored.Capabilities)
	if isNew {
		assert.Contains(t, caps, "review_mode")
		assert.Contains(t, caps, "rule:http_header")
		assert.Contains(t, caps, "protocol:postgres")
	} else {
		assert.NotContains(t, strings.Join(caps, ","), "rule:", "a release before this branch sends no rule: entry: %v", caps)
	}
	t.Logf("%s booted; version=%q capabilities=%v", label, row.Version, caps)

	// First heartbeat: the boot document counts as applied.
	row = waitRow(t, sc.ID, 90*time.Second, "the first heartbeat", func(r openapi.SidecarResponse) bool {
		return r.ConfigState == configStateApplied
	})
	assert.Equal(t, row.ServedRevision, row.AppliedRevision)

	// A document this build refuses, or cannot take on without a restart.
	// trust is unknown to a release before 1.198.0; a build that knows it
	// cannot swap it live.
	w, _ = putConfig(t, sc.ID, listenerDoc(port, `,"trust":{"ca_file":"/nonexistent/e2e-ca.pem"}`))
	require.Equal(t, http.StatusOK, w.Code, "put trust: %s", w.Body)
	row = waitRow(t, sc.ID, 150*time.Second, "the refusal to reach the row", func(r openapi.SidecarResponse) bool {
		return r.ConfigState == configStateRefused || r.ConfigState == configStateRestart
	})
	t.Logf("%s reported %s: %q", label, row.ConfigState, row.LastError)
	if isNew {
		assert.NotEmpty(t, row.LastError, "a build of this branch says why")
	} else {
		assert.Equal(t, configStateRefused, row.ConfigState)
		assert.Empty(t, row.LastError, "a release before this branch has no last_error")
	}
	held := row.ConfigState
	// Held: a release before this branch reports "unchanged" one tick later
	// and adopts the served revision; the row must not read converged.
	time.Sleep(75 * time.Second)
	row = getRow(t, sc.ID)
	assert.Equal(t, held, row.ConfigState, "the refusal decayed: outcome=%q applied=%q served=%q",
		row.LastOutcome, row.AppliedRevision, row.ServedRevision)
	assert.NotEqual(t, row.ServedRevision, row.AppliedRevision, "the refused revision was adopted")

	// An apply replaces it.
	w, _ = putConfig(t, sc.ID, listenerDoc(port, ""))
	require.Equal(t, http.StatusOK, w.Code, "put back: %s", w.Body)
	row = waitRow(t, sc.ID, 150*time.Second, "the good document to apply", func(r openapi.SidecarResponse) bool {
		return r.ConfigState == configStateApplied
	})
	assert.Empty(t, row.LastError)

	if isNew {
		// Rule-only drift still swaps live.
		w, _ = putConfig(t, sc.ID, fmt.Sprintf(`{"listeners":[{"name":"appdb","protocol":"postgres","listen":"127.0.0.1:%d","upstream":"127.0.0.1:5432",`+
			`"guardrails":{"rules":[{"name":"words","type":"deny_words_list","words":["drop table"]}]}}]}`, port))
		require.Equal(t, http.StatusOK, w.Code, "put rules: %s", w.Body)
		served := row.ServedRevision
		row = waitRow(t, sc.ID, 150*time.Second, "the rule drift to apply live", func(r openapi.SidecarResponse) bool {
			return r.ConfigState == configStateApplied && r.ServedRevision != served
		})
		assert.Contains(t, p.logText(t), "configuration applied")
		if exited, _ := p.exited(time.Second); exited {
			t.Fatalf("%s exited on a rule-only drift:\n%s", label, p.logText(t))
		}
		return
	}

	// A boot on a refused document exits, reports nothing, and the row reads
	// not applied once the served revision has gone unreported long enough.
	exited, _ := p.exited(0)
	require.False(t, exited)
	p.cmd.Process.Kill()
	<-p.done
	p.done <- nil
	w, _ = putConfig(t, sc.ID, listenerDoc(port, `,"trust":{"ca_file":"/nonexistent/e2e-ca.pem"}`))
	require.Equal(t, http.StatusOK, w.Code, "put trust again: %s", w.Body)
	p2 := startSidecar(t, bin, srv.URL, token, filepath.Join(dir, "reboot.log"))
	exited, _ = p2.exited(30 * time.Second)
	require.True(t, exited, "%s kept running on a document it cannot load:\n%s", label, p2.logText(t))
	assert.Contains(t, p2.logText(t), `unknown field "trust"`)
	row = getRow(t, sc.ID)
	assert.Equal(t, configStateApplying, row.ConfigState, "just served: outcome=%q", row.LastOutcome)
	require.NoError(t, models.DB.Exec(`UPDATE private.sidecars SET served_revision_at = now() - interval '10 minutes' WHERE id = ?`, sc.ID).Error)
	assert.Equal(t, configStateNotApplied, getRow(t, sc.ID).ConfigState)
	assert.True(t, slices.Contains([]string{"applied", "unchanged"}, row.LastOutcome), "the held outcome is what the last live process reported: %q", row.LastOutcome)
}
