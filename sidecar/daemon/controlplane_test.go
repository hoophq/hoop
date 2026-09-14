package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/license/licensetest"
)

// planeConfig is what a healthy control plane answers: one postgres lane,
// the same shape an operator stores on the gateway's sidecar row.
const planeConfig = `{"listeners":[{"name":"appdb","protocol":"postgres","listen":":1","upstream":"h:5432"}]}`

// handshakeCall records what the fake plane saw, so a test can assert the
// token and version that arrived rather than only the outcome.
type handshakeCall struct {
	token   string
	version string
	path    string
}

// planeServer serves the handshake with body, capturing each call. A nil
// check leaves the default 200 handler.
func planeServer(t *testing.T, status int, body string) (*httptest.Server, *[]handshakeCall) {
	t.Helper()
	calls := &[]handshakeCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Version string `json:"version"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		*calls = append(*calls, handshakeCall{
			token:   r.Header.Get(sidecarTokenHeader),
			version: req.Version,
			path:    r.URL.Path,
		})
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

func TestSetupFetchesTheConfigFromTheControlPlane(t *testing.T) {
	srv, calls := planeServer(t, http.StatusOK, planeConfig)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_env")

	// No config file at all: the env pair is the whole configuration.
	cfg, _, err := SetupWith("", nil, nil)
	if err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if len(cfg.Listeners) != 1 || cfg.Listeners[0].Name != "appdb" {
		t.Fatalf("the plane's config did not become the running config: %+v", cfg.Listeners)
	}
	if url, source, ok := cfg.ControlPlane(); !ok || url != srv.URL || source != ControlPlaneURLEnv {
		t.Errorf("ControlPlane() = %q, %q, %v", url, source, ok)
	}
	if len(*calls) != 1 {
		t.Fatalf("handshakes = %d, want 1", len(*calls))
	}
	got := (*calls)[0]
	if got.token != "hsc_env" {
		t.Errorf("token = %q", got.token)
	}
	if got.version != Version {
		t.Errorf("version = %q, want %q", got.version, Version)
	}
	if got.path != controlPlaneHandshakePath {
		t.Errorf("path = %q", got.path)
	}
}

func TestTheEnvironmentOutranksTheConfigKeyForTheControlPlaneURL(t *testing.T) {
	fromKey, _ := planeServer(t, http.StatusOK,
		`{"listeners":[{"name":"from-key","protocol":"postgres","listen":":1","upstream":"h:5432"}]}`)
	fromEnv, _ := planeServer(t, http.StatusOK,
		`{"listeners":[{"name":"from-env","protocol":"postgres","listen":":1","upstream":"h:5432"}]}`)
	t.Setenv(ControlPlaneURLEnv, fromEnv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")

	cfg, _, err := SetupWith(writeConfig(t, `{"control_plane_url":"`+fromKey.URL+`"}`), nil, nil)
	if err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if cfg.Listeners[0].Name != "from-env" {
		t.Fatalf("the config key won over the environment: %q", cfg.Listeners[0].Name)
	}
}

// First wins, not first valid: a broken env var is an error, never a reason
// to fall through to the config key. Same rule the license sources follow.
func TestABrokenControlPlaneEnvDoesNotFallThrough(t *testing.T) {
	srv, calls := planeServer(t, http.StatusOK, planeConfig)
	t.Setenv(ControlPlaneURLEnv, "not a url")
	t.Setenv(SidecarTokenEnv, "hsc_x")

	_, _, err := SetupWith(writeConfig(t, `{"control_plane_url":"`+srv.URL+`"}`), nil, nil)
	if err == nil {
		t.Fatal("a broken env URL fell through to the config key")
	}
	if !strings.Contains(err.Error(), ControlPlaneURLEnv) {
		t.Errorf("the error does not name the source: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("the plane named by the config key was contacted %d time(s)", len(*calls))
	}
}

// A URL carrying a query, fragment or userinfo would build a request whose
// path is not the handshake's, so validation refuses each one and names the
// source the operator has to fix.
func TestAControlPlaneURLCarriesNoQueryFragmentOrUser(t *testing.T) {
	t.Setenv(SidecarTokenEnv, "hsc_x")
	for _, bad := range []string{
		"http://plane.example?tenant=a",
		"http://plane.example#frag",
		"http://user:pw@plane.example",
	} {
		t.Setenv(ControlPlaneURLEnv, bad)
		_, _, err := SetupWith("", nil, nil)
		if err == nil {
			t.Fatalf("%q was accepted", bad)
		}
		if !strings.Contains(err.Error(), ControlPlaneURLEnv) {
			t.Errorf("the error for %q does not name the source: %v", bad, err)
		}
	}
}

// A plane behind a path prefix keeps it: the handshake path joins the
// configured base instead of replacing it.
func TestAPathPrefixedPlaneURLKeepsItsPrefix(t *testing.T) {
	srv, calls := planeServer(t, http.StatusOK, planeConfig)
	t.Setenv(ControlPlaneURLEnv, srv.URL+"/hoop/")
	t.Setenv(SidecarTokenEnv, "hsc_x")

	if _, _, err := SetupWith("", nil, nil); err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if got := (*calls)[0].path; got != "/hoop"+controlPlaneHandshakePath {
		t.Errorf("path = %q, want the prefix kept", got)
	}
}

// The token rides a custom header, which Go forwards on redirects even
// across origins. The handshake never follows one: the 3xx surfaces as an
// error naming the Location, and the token goes nowhere else.
func TestARedirectingPlaneIsRefusedWithoutResendingTheToken(t *testing.T) {
	leaked := false
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = true
	}))
	t.Cleanup(sink.Close)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sink.URL, http.StatusMovedPermanently)
	}))
	t.Cleanup(redirector.Close)
	t.Setenv(ControlPlaneURLEnv, redirector.URL)
	t.Setenv(SidecarTokenEnv, "hsc_secret")

	_, _, err := SetupWith("", nil, nil)
	if err == nil {
		t.Fatal("a redirecting plane was accepted")
	}
	if !strings.Contains(err.Error(), "redirected") {
		t.Errorf("the error does not say it was a redirect: %v", err)
	}
	if leaked {
		t.Fatal("the redirect target was contacted; the token traveled")
	}
}

func TestTheTokenFlagOutranksTheEnvironment(t *testing.T) {
	srv, calls := planeServer(t, http.StatusOK, planeConfig)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_env")

	if _, _, err := SetupWith("", nil, nil, WithControlPlaneToken("hsc_flag")); err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if got := (*calls)[0].token; got != "hsc_flag" {
		t.Errorf("token = %q, want the flag value", got)
	}
}

// An empty flag is the same as no flag, so an entry point hands its variable
// through without branching on whether the operator set it.
func TestAnEmptyTokenFlagFallsThroughToTheEnvironment(t *testing.T) {
	srv, calls := planeServer(t, http.StatusOK, planeConfig)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_env")

	if _, _, err := SetupWith("", nil, nil, WithControlPlaneToken("")); err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if got := (*calls)[0].token; got != "hsc_env" {
		t.Errorf("token = %q, want the env value", got)
	}
}

func TestAControlPlaneWithoutATokenStopsStartup(t *testing.T) {
	srv, calls := planeServer(t, http.StatusOK, planeConfig)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "")

	_, _, err := SetupWith("", nil, nil)
	if err == nil {
		t.Fatal("a control plane with no token was accepted")
	}
	for _, want := range []string{"token flag", SidecarTokenEnv} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name the fix %q: %v", want, err)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("the plane was contacted without a token %d time(s)", len(*calls))
	}
}

// A token that configures nothing will surprise whoever set it the day they
// rely on it, so Setup refuses it rather than ignoring it.
func TestATokenWithoutAControlPlaneStopsStartup(t *testing.T) {
	t.Setenv(ControlPlaneURLEnv, "")
	t.Setenv(SidecarTokenEnv, "hsc_orphan")

	_, _, err := SetupWith(writeConfig(t, minimalConfig+`}`), nil, nil)
	if err == nil {
		t.Fatal("an orphan token was accepted")
	}
	if !strings.Contains(err.Error(), SidecarTokenEnv) {
		t.Errorf("the error does not name the source: %v", err)
	}
}

// adoptivePlane is a fake gateway with the import route, matching the real
// handlers' contract: the handshake answers 412 (or, as a legacy build, 200
// with the empty document) until something is stored, and the PUT stores a
// document exactly once, answering 409 afterwards.
type adoptivePlane struct {
	mu     sync.Mutex
	stored string
	legacy bool
	puts   []string
	srv    *httptest.Server
}

func newAdoptivePlane(t *testing.T, stored string, legacy bool) *adoptivePlane {
	t.Helper()
	p := &adoptivePlane{stored: stored, legacy: legacy}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == controlPlaneHandshakePath:
			if p.stored == "" {
				if p.legacy {
					_, _ = w.Write([]byte(`{}`))
					return
				}
				w.WriteHeader(http.StatusPreconditionFailed)
				_, _ = w.Write([]byte(`{"message":"no configuration is assigned to this sidecar"}`))
				return
			}
			_, _ = w.Write([]byte(p.stored))
		case r.Method == http.MethodPut && r.URL.Path == controlPlaneConfigurationPath:
			body, _ := io.ReadAll(r.Body)
			p.puts = append(p.puts, string(body))
			if p.stored != "" {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"message":"already configured"}`))
				return
			}
			p.stored = string(body)
			_, _ = w.Write([]byte(p.stored))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(p.srv.Close)
	return p
}

// The connect journey: a standalone sidecar's file, plus the URL and the
// token, seeds a plane that holds no configuration, and the plane's answer
// after the import is what runs. The pushed document carries no
// control_plane_url and no license: the URL is connection metadata this
// process resolved, and the license stays a file-side source.
func TestAConfigFileSeedsAPlaneWithNoConfiguration(t *testing.T) {
	plane := newAdoptivePlane(t, "", false)
	t.Setenv(ControlPlaneURLEnv, plane.srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")

	cfg, _, err := SetupWith(writeConfig(t, minimalConfig+`,"log_level":"debug"}`), nil, nil)
	if err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if len(cfg.Listeners) != 1 {
		t.Fatalf("listeners = %d, want the file's one", len(cfg.Listeners))
	}
	if len(plane.puts) != 1 {
		t.Fatalf("imports = %d, want 1", len(plane.puts))
	}
	if strings.Contains(plane.puts[0], "control_plane_url") || strings.Contains(plane.puts[0], "license") {
		t.Errorf("the pushed document leaks a file-side fact: %s", plane.puts[0])
	}
	if !strings.Contains(plane.puts[0], `"log_level":"debug"`) {
		t.Errorf("the pushed document lost a field: %s", plane.puts[0])
	}
	if cfg.cp == nil || !cfg.cp.imported || cfg.cp.fileListeners != 0 {
		t.Errorf("cp bookkeeping: imported=%v fileListeners=%d, want true/0",
			cfg.cp != nil && cfg.cp.imported, cfg.cp.fileListeners)
	}

	// The restart: the plane now owns the config. The file's listeners are
	// ignored, not re-imported and not refused.
	cfg2, _, err := SetupWith(writeConfig(t, minimalConfig+`}`), nil, nil)
	if err != nil {
		t.Fatalf("SetupWith after the import: %v", err)
	}
	if len(plane.puts) != 1 {
		t.Fatalf("imports after the restart = %d, want still 1", len(plane.puts))
	}
	if cfg2.cp.imported || cfg2.cp.fileListeners != 1 {
		t.Errorf("cp bookkeeping after the restart: imported=%v fileListeners=%d, want false/1",
			cfg2.cp.imported, cfg2.cp.fileListeners)
	}
}

// Once the plane holds a configuration it owns it: the file's listeners are
// ignored rather than merged or refused, and the count reaches Run's warn.
// Never merged: no operator can predict which half of a merged config wins.
func TestAPlaneWithAConfigWinsOverTheFileListeners(t *testing.T) {
	plane := newAdoptivePlane(t, planeConfig, false)
	t.Setenv(ControlPlaneURLEnv, plane.srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")

	cfg, _, err := SetupWith(writeConfig(t, minimalConfig+`}`), nil, nil)
	if err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if len(cfg.Listeners) != 1 || cfg.Listeners[0].Name != "appdb" {
		t.Fatalf("the plane's config did not win: %+v", cfg.Listeners)
	}
	if len(plane.puts) != 0 {
		t.Fatalf("imports = %d, want 0", len(plane.puts))
	}
	if cfg.cp.imported || cfg.cp.fileListeners != 1 {
		t.Errorf("cp bookkeeping: imported=%v fileListeners=%d, want false/1",
			cfg.cp.imported, cfg.cp.fileListeners)
	}
}

// An older gateway answers the empty document with a 200 instead of the 412.
// Same fact, same import; without the probe this build would blame a schema
// mismatch for what is really an unconfigured sidecar.
func TestALegacyEmptyAnswerTriggersTheImport(t *testing.T) {
	plane := newAdoptivePlane(t, "", true)
	t.Setenv(ControlPlaneURLEnv, plane.srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")

	cfg, _, err := SetupWith(writeConfig(t, minimalConfig+`}`), nil, nil)
	if err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if len(cfg.Listeners) != 1 || len(plane.puts) != 1 {
		t.Fatalf("listeners = %d, imports = %d, want 1 and 1", len(cfg.Listeners), len(plane.puts))
	}
}

// A configuration authored between the handshake and the push wins the race:
// the plane answers 409, and this boot serves the concurrent author's
// document instead of failing or overwriting it.
func TestAConcurrentlyAuthoredConfigWinsTheImportRace(t *testing.T) {
	var mu sync.Mutex
	handshakes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == controlPlaneHandshakePath:
			handshakes++
			if handshakes == 1 {
				w.WriteHeader(http.StatusPreconditionFailed)
				_, _ = w.Write([]byte(`{"message":"no configuration is assigned to this sidecar"}`))
				return
			}
			_, _ = w.Write([]byte(planeConfig))
		case r.Method == http.MethodPut && r.URL.Path == controlPlaneConfigurationPath:
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"already configured"}`))
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")

	cfg, _, err := SetupWith(writeConfig(t, minimalConfig+`}`), nil, nil)
	if err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if len(cfg.Listeners) != 1 || cfg.Listeners[0].Name != "appdb" {
		t.Fatalf("the concurrent author's config did not win: %+v", cfg.Listeners)
	}
	if cfg.cp.imported {
		t.Error("a lost race must not claim the import")
	}
}

// A plane with nothing to serve and no file to import from is a dead end the
// error must name, with both ways out.
func TestAnEmptyPlaneWithoutAFileStopsStartup(t *testing.T) {
	plane := newAdoptivePlane(t, "", false)
	t.Setenv(ControlPlaneURLEnv, plane.srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")

	_, _, err := SetupWith("", nil, nil)
	if err == nil {
		t.Fatal("an empty plane with no file was accepted")
	}
	if !strings.Contains(err.Error(), "has no configuration") {
		t.Errorf("the error does not name the problem: %v", err)
	}
}

// A file whose whole job is naming the plane must load even though it has no
// listeners of its own.
func TestAPlanePointingFileNeedsNoListeners(t *testing.T) {
	srv, _ := planeServer(t, http.StatusOK, planeConfig)
	t.Setenv(SidecarTokenEnv, "hsc_x")
	t.Setenv(ControlPlaneURLEnv, "")

	cfg, _, err := SetupWith(writeConfig(t, `{"control_plane_url":"`+srv.URL+`"}`), nil, nil)
	if err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if len(cfg.Listeners) != 1 {
		t.Fatalf("listeners = %d", len(cfg.Listeners))
	}
	if _, source, _ := cfg.ControlPlane(); !strings.Contains(source, "control_plane_url") {
		t.Errorf("source = %q, want the config key", source)
	}
}

// The plane sends no license today, so the file's "license" key stays a
// license source in plane mode; the documented precedence is unchanged.
func TestTheFileLicenseKeySurvivesThePlaneConfig(t *testing.T) {
	srv, _ := planeServer(t, http.StatusOK, planeConfig)
	t.Setenv(SidecarTokenEnv, "hsc_x")
	t.Setenv(ControlPlaneURLEnv, "")
	doc := licensetest.Document(t, licensetest.Enterprise())

	body, err := json.Marshal(map[string]string{"control_plane_url": srv.URL, "license": doc})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := SetupWith(writeConfig(t, string(body)), nil, nil)
	if err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if cfg.Licensing().State() != license.StateValid {
		t.Fatalf("license state = %q, want valid", cfg.Licensing().State())
	}
}

func TestARejectedTokenNamesTheProblem(t *testing.T) {
	srv, _ := planeServer(t, http.StatusUnauthorized, `{"message":"access denied"}`)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_wrong")

	_, _, err := SetupWith("", nil, nil)
	if err == nil {
		t.Fatal("a 401 was accepted")
	}
	if !strings.Contains(err.Error(), "rejected the token") {
		t.Errorf("the error does not say the token was rejected: %v", err)
	}
}

// A 422 is an operator misconfiguration on the plane side; the message names
// what to change and must reach whoever reads the startup failure.
func TestAPlaneMisconfigurationReachesTheOperator(t *testing.T) {
	srv, _ := planeServer(t, http.StatusUnprocessableEntity,
		`{"message":"no connections are assigned to this sidecar"}`)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")

	_, _, err := SetupWith("", nil, nil)
	if err == nil {
		t.Fatal("a 422 was accepted")
	}
	if !strings.Contains(err.Error(), "no connections are assigned") {
		t.Errorf("the plane's message was lost: %v", err)
	}
}

// A listener-less 200 answer with no file to import routes into the same
// "has no configuration" error, never into the strict decoder's schema
// blame: what stands between an empty answer and a process serving nothing
// is the import path's own check.
func TestAnEmptyPlaneConfigIsRefused(t *testing.T) {
	srv, _ := planeServer(t, http.StatusOK, `{"listeners":[]}`)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")

	_, _, err := SetupWith("", nil, nil)
	if err == nil {
		t.Fatal("a config with no listeners was accepted from the plane")
	}
	if !strings.Contains(err.Error(), "has no configuration") {
		t.Errorf("the error does not say what is missing: %v", err)
	}
}

// One edit is handled once, not once per tick: the reloader remembers the
// last document that reached a terminal outcome, and the same bytes on the
// next tick do nothing. reload_test.go covers what handling decides.
func TestTheSameDocumentIsHandledOnce(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)

	drifted := editJSON(t, reloadBase, `"words": ["drop table"]`, `"words": ["truncate"]`)
	if got := handleWith(rl, buf, drifted); got != reloadApplied {
		t.Fatalf("first handle = %v, want applied; log:\n%s", got, buf)
	}
	if got := handleWith(rl, buf, drifted); got != reloadUnchanged {
		t.Fatalf("second handle = %v, want unchanged; log:\n%s", got, buf)
	}
	if rl.gen != 1 {
		t.Fatalf("generation = %d; the duplicate was re-applied", rl.gen)
	}
}

// diskAnswer is what the plane sends a sidecar whose configuration it has
// released: the instruction and nothing that could configure a lane.
const diskAnswer = `{"listeners":null,"audit":{"file":""},"admin":{"listen":""},"log_level":"","load_from_disk":true}`

// localFileConfig declares the one lane a disk-mode sidecar must keep
// serving, under a name no plane answer in this file uses.
const localFileConfig = `{"listeners":[{"name":"localdb","protocol":"postgres","listen":":1","upstream":"h:5432"}]}`

// The plane released the configuration: the file's listeners run, the plane
// is still connected for the heartbeat, and nothing was imported — a
// listener-less answer must not be mistaken for an unconfigured sidecar.
func TestADiskModeAnswerRunsTheLocalFile(t *testing.T) {
	srv, calls := planeServer(t, http.StatusOK, diskAnswer)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")

	cfg, _, err := SetupWith(writeConfig(t, localFileConfig), nil, nil)
	if err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if len(cfg.Listeners) != 1 || cfg.Listeners[0].Name != "localdb" {
		t.Fatalf("the file's listeners did not become the running config: %+v", cfg.Listeners)
	}
	if url, _, ok := cfg.ControlPlane(); !ok || url != srv.URL {
		t.Errorf("ControlPlane() = %q, %v; a disk-mode sidecar stays registered", url, ok)
	}
	if !cfg.cp.diskMode {
		t.Error("diskMode is not set, so Run and the heartbeat cannot tell the source")
	}
	if cfg.cp.fileListeners != 0 {
		t.Errorf("fileListeners = %d; Run would warn that listeners it is serving are ignored",
			cfg.cp.fileListeners)
	}
	if len(*calls) != 1 {
		t.Fatalf("plane calls = %v, want one handshake and no import", *calls)
	}
	if (*calls)[0].path != controlPlaneHandshakePath {
		t.Errorf("path = %q, want the handshake", (*calls)[0].path)
	}
}

func TestDiskModeWithoutAConfigFileStopsStartup(t *testing.T) {
	srv, _ := planeServer(t, http.StatusOK, diskAnswer)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")

	_, _, err := SetupWith("", nil, nil)
	if err == nil {
		t.Fatal("a disk-mode answer with no config file was accepted")
	}
	if !strings.Contains(err.Error(), "no config file was given") {
		t.Errorf("the error does not name the missing file: %v", err)
	}
}

// A file whose only job was naming the plane has nothing to run once the
// plane releases the configuration, and says so.
func TestDiskModeWithAListenerLessFileStopsStartup(t *testing.T) {
	srv, _ := planeServer(t, http.StatusOK, diskAnswer)
	t.Setenv(SidecarTokenEnv, "hsc_x")
	t.Setenv(ControlPlaneURLEnv, "")

	_, _, err := SetupWith(writeConfig(t, `{"control_plane_url":"`+srv.URL+`"}`), nil, nil)
	if err == nil {
		t.Fatal("a disk-mode answer with a listener-less file was accepted")
	}
	if !strings.Contains(err.Error(), "declares no listeners") {
		t.Errorf("the error does not name the problem: %v", err)
	}
}

// The license the plane sends in disk mode outranks the file's "license"
// key. Only the last document licensetest signs verifies (the trust root is
// process-wide), so a wrong precedence here fails startup outright instead
// of quietly reporting the file's license.
func TestThePlaneLicenseOutranksTheFileKeyInDiskMode(t *testing.T) {
	fileDoc := licensetest.Document(t, licensetest.Enterprise())
	planePayload := licensetest.Enterprise()
	planePayload.Description = "from the plane"
	planeDoc := licensetest.Document(t, planePayload)

	answer, err := json.Marshal(map[string]any{"load_from_disk": true, "license": planeDoc})
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := planeServer(t, http.StatusOK, string(answer))
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")

	file, err := json.Marshal(map[string]any{
		"listeners": []map[string]string{
			{"name": "localdb", "protocol": "postgres", "listen": ":1", "upstream": "h:5432"},
		},
		"license": fileDoc,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := SetupWith(writeConfig(t, string(file)), nil, nil)
	if err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if cfg.Licensing().State() != license.StateValid {
		t.Fatalf("license state = %q, want valid", cfg.Licensing().State())
	}
	if got := cfg.Licensing().License.Payload.Description; got != "from the plane" {
		t.Errorf("license description = %q; the plane's license did not win", got)
	}
}

// The entry belongs to the plane. A file carrying it is refused rather than
// ignored: two ways to say the same thing is two ways to get it wrong.
func TestADiskModeFlagInTheConfigFileIsRefused(t *testing.T) {
	srv, calls := planeServer(t, http.StatusOK, planeConfig)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")

	_, _, err := SetupWith(writeConfig(t, `{"load_from_disk":true,`+
		`"listeners":[{"name":"localdb","protocol":"postgres","listen":":1","upstream":"h:5432"}]}`), nil, nil)
	if err == nil {
		t.Fatal("a config file setting load_from_disk was accepted")
	}
	if !strings.Contains(err.Error(), "set by the control plane") {
		t.Errorf("the error does not say who sets it: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("the plane was contacted %d time(s) despite the refusal", len(*calls))
	}
}

// A file may not carry the key at all, in either spelling: "false" beside a
// plane that says true is the contradiction that would otherwise run from
// disk while the file asked for the opposite.
func TestADiskModeFlagSetFalseInTheConfigFileIsRefused(t *testing.T) {
	srv, calls := planeServer(t, http.StatusOK, planeConfig)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")

	_, _, err := SetupWith(writeConfig(t, `{"load_from_disk":false,`+
		`"listeners":[{"name":"localdb","protocol":"postgres","listen":":1","upstream":"h:5432"}]}`), nil, nil)
	if err == nil {
		t.Fatal(`a config file setting load_from_disk to false was accepted`)
	}
	if !strings.Contains(err.Error(), "set by the control plane") {
		t.Errorf("the error does not say who sets it: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("the plane was contacted %d time(s) despite the refusal", len(*calls))
	}
}

// An admin can turn the flag on between the startup handshake and the import
// push. The plane refuses the push and serves the disk answer; the boot runs
// its file rather than reporting a plane that "still answers without
// listeners".
func TestAFlipDuringTheImportRunsTheLocalFile(t *testing.T) {
	var mu sync.Mutex
	flipped := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPut:
			// The flag landed just now, so the plane will not adopt.
			flipped = true
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"this sidecar loads its configuration from disk"}`))
		case flipped:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(diskAnswer))
		default:
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = w.Write([]byte(`{"message":"no configuration is assigned to this sidecar"}`))
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")

	cfg, _, err := SetupWith(writeConfig(t, localFileConfig), nil, nil)
	if err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if len(cfg.Listeners) != 1 || cfg.Listeners[0].Name != "localdb" {
		t.Fatalf("the file's listeners did not become the running config: %+v", cfg.Listeners)
	}
	if !cfg.cp.diskMode {
		t.Error("diskMode is not set after the flip was discovered by the import")
	}
}

// The plane keeps the config file it was handed, so a Loader that caches its
// result must not come back holding anything this boot resolved.
func TestADiskModeBootDoesNotMutateTheLoadedConfig(t *testing.T) {
	srv, _ := planeServer(t, http.StatusOK, diskAnswer)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")

	local, err := LoadConfigBytes([]byte(localFileConfig))
	if err != nil {
		t.Fatalf("LoadConfigBytes: %v", err)
	}
	before, err := json.Marshal(local)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := SetupWith("cached", func(string) (*Config, error) { return local, nil }, nil)
	if err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if cfg == local {
		t.Fatal("the running config is the loader's own value; a second Setup would inherit this boot")
	}
	after, err := json.Marshal(local)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("the loader's config was rewritten by this boot:\nbefore %s\nafter  %s", before, after)
	}
	if local.cp != nil {
		t.Error("the loader's config carries this boot's connection")
	}
}

func testLogger() (*slog.Logger, *strings.Builder) {
	var b strings.Builder
	return slog.New(slog.NewTextHandler(&b, nil)), &b
}

// The plane taking the configuration back reaches the running lanes: its
// document goes to the reloader like any other drift, so rules the plane
// authored apply without a restart. The answer carries load_from_disk
// explicitly, which must not read as a change beyond the rules.
func TestATakeoverAppliesThePlanesRules(t *testing.T) {
	rl, _ := testReloader(t, reloadBase)
	log, out := testLogger()
	cp := &controlPlane{url: "http://plane", token: "hsc_x", diskMode: true, fileDoc: []byte(reloadBase)}

	drifted := editJSON(t, reloadBase, `"words": ["drop table"]`, `"words": ["truncate"]`)
	drifted = editJSON(t, drifted, `"log_level": "info"`, `"log_level": "info", "load_from_disk": false`)
	cp.handleAnswer(log, rl, []byte(drifted), nil)
	if rl.gen != 1 {
		t.Fatalf("generation = %d; the plane's rules did not reach the running lanes; log:\n%s", rl.gen, out)
	}
	if cp.diskMode {
		t.Error("the process still reports the config file as its source")
	}
	// And the same answer again is one document, handled once.
	cp.handleAnswer(log, rl, []byte(drifted), nil)
	if rl.gen != 1 {
		t.Errorf("generation = %d; the unchanged document was re-applied", rl.gen)
	}
}

// The release reaches them too, from the other side: the file's own document
// goes to the reloader, so the rules the file declares apply without a
// restart.
func TestAReleaseAppliesTheConfigFile(t *testing.T) {
	rl, _ := testReloader(t, reloadBase)
	log, out := testLogger()
	// A plane-owned boot whose file differs from the plane's document in its
	// rules alone, which is what a live process can swap.
	fileDoc := editJSON(t, reloadBase, `"words": ["drop table"]`, `"words": ["truncate"]`)
	cp := &controlPlane{url: "http://plane", token: "hsc_x", fileDoc: []byte(fileDoc)}

	cp.handleAnswer(log, rl, []byte(diskAnswer), nil)
	if rl.gen != 1 {
		t.Fatalf("generation = %d; the config file's rules did not reach the running lanes; log:\n%s", rl.gen, out)
	}
	if !cp.diskMode {
		t.Error("the process does not report the config file as its source")
	}
	if rl.lastHandled == nil {
		t.Error("the reloader was not told which document is now running")
	}
}

// Nothing to switch to: a process started without a config file cannot obey
// a release, and says what would make it possible.
func TestAReleaseWithoutAConfigFileAsksForOne(t *testing.T) {
	rl, _ := testReloader(t, reloadBase)
	log, out := testLogger()
	cp := &controlPlane{url: "http://plane", token: "hsc_x"}

	cp.handleAnswer(log, rl, []byte(diskAnswer), nil)
	cp.handleAnswer(log, rl, []byte(diskAnswer), nil)
	if rl.gen != 0 {
		t.Fatalf("generation = %d; something was applied with no config file to apply", rl.gen)
	}
	if got := strings.Count(out.String(), "without a config file"); got != 1 {
		t.Errorf("the advice was logged %d time(s), want once; log:\n%s", got, out)
	}

	// The plane taking it back makes that advice stale, so the next release
	// has to say it again rather than pass in silence.
	cp.handleAnswer(log, rl, []byte(reloadBase), nil)
	cp.handleAnswer(log, rl, []byte(diskAnswer), nil)
	if got := strings.Count(out.String(), "without a config file"); got != 2 {
		t.Errorf("the second release advised %d time(s) in total, want 2; log:\n%s", got, out)
	}
}

// A release this process can only half apply keeps ADR-0014's boundary: the
// lanes it cannot rebuild stay, and the operator is told what a restart
// would change.
func TestAReleaseBeyondTheRulesAsksForARestart(t *testing.T) {
	rl, _ := testReloader(t, reloadBase)
	log, out := testLogger()
	fileDoc := editJSON(t, reloadBase, `"upstream": "h:5432"`, `"upstream": "other:5432"`)
	cp := &controlPlane{url: "http://plane", token: "hsc_x", fileDoc: []byte(fileDoc)}

	cp.handleAnswer(log, rl, []byte(diskAnswer), nil)
	if rl.gen != 0 {
		t.Fatalf("generation = %d; a lane was rebuilt live; log:\n%s", rl.gen, out)
	}
	if !strings.Contains(out.String(), "restart to apply it") {
		t.Errorf("the operator was not told a restart applies it; log:\n%s", out)
	}
}

// A disk-mode boot is already serving its file, so the first heartbeat that
// repeats the same answer must say nothing at all.
func TestADiskModeBootIsQuietUntilSomethingMoves(t *testing.T) {
	rl, _ := testReloader(t, reloadBase)
	log, out := testLogger()
	cp := &controlPlane{url: "http://plane", token: "hsc_x", diskMode: true, fileDoc: []byte(reloadBase)}

	cp.handleAnswer(log, rl, []byte(diskAnswer), nil)
	if out.String() != "" {
		t.Errorf("an unchanged answer produced output:\n%s", out)
	}
	if rl.gen != 0 {
		t.Errorf("generation = %d; the running document was re-applied", rl.gen)
	}
}

// A license the plane moved is the one thing a running process cannot take,
// whichever side owns the configuration when it moves.
func TestAMovedLicenseAsksForARestart(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer string
	}{
		{"released", `{"load_from_disk":true,"license":"new"}`},
		{"taken back", editJSON(t, reloadBase, `"log_level": "info"`, `"log_level": "info", "license": "new"`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rl, _ := testReloader(t, reloadBase)
			log, out := testLogger()
			cp := &controlPlane{url: "http://plane", token: "hsc_x", diskMode: true,
				fileDoc: []byte(reloadBase), planeLicense: "old"}

			cp.handleAnswer(log, rl, []byte(tc.answer), nil)
			if got := strings.Count(out.String(), "the license the control plane sends changed"); got != 1 {
				t.Errorf("the moved license was reported %d time(s), want once; log:\n%s", got, out)
			}
			// Said once: the license does not move again on its own.
			cp.handleAnswer(log, rl, []byte(tc.answer), nil)
			if got := strings.Count(out.String(), "the license the control plane sends changed"); got != 1 {
				t.Errorf("the same license was reported %d time(s); log:\n%s", got, out)
			}
		})
	}
}

// A plane that took the configuration back and holds nothing yet answers
// 412. There is no document to apply, so the operator reads what will
// happen instead of a failed handshake.
func TestADiskModeBootReadsA412AsTheTakeover(t *testing.T) {
	rl, _ := testReloader(t, reloadBase)
	log, out := testLogger()
	cp := &controlPlane{url: "http://plane", token: "hsc_x", diskMode: true, fileDoc: []byte(reloadBase)}

	cp.handleAnswer(log, rl, nil, fmt.Errorf("%w (at %s)", errPlaneHasNoConfig, "http://plane"))
	if !strings.Contains(out.String(), "took this sidecar's configuration back but holds none") {
		t.Errorf("the 412 was not read as the takeover; log:\n%s", out)
	}
	if strings.Contains(out.String(), "handshake failed") {
		t.Errorf("the 412 was reported as a failed handshake; log:\n%s", out)
	}
}

// Every other failure keeps degrading the way it did: the lanes run on and
// the operator hears about the plane.
func TestAFailedHandshakeStillDegradesInDiskMode(t *testing.T) {
	rl, _ := testReloader(t, reloadBase)
	log, out := testLogger()
	cp := &controlPlane{url: "http://plane", token: "hsc_x", diskMode: true}

	cp.handleAnswer(log, rl, nil, fmt.Errorf("connection refused"))
	if !strings.Contains(out.String(), "handshake failed") {
		t.Errorf("a transport failure was not reported; log:\n%s", out)
	}
	if strings.Contains(out.String(), "took this sidecar's configuration back") {
		t.Errorf("a transport failure was read as a source flip; log:\n%s", out)
	}
}

// The plane's license is labeled as the plane's in diagnostics, not as the
// file's "license" key: an operator told to fix a license must be sent to the
// right source. Disk mode is the path that folded the two together.
func TestThePlaneLicenseIsLabeledAsThePlanes(t *testing.T) {
	planePayload := licensetest.Enterprise()
	planePayload.Description = "from the plane"
	planeDoc := licensetest.Document(t, planePayload)

	answer, err := json.Marshal(map[string]any{"load_from_disk": true, "license": planeDoc})
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := planeServer(t, http.StatusOK, string(answer))
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")

	cfg, _, err := SetupWith(writeConfig(t, localFileConfig), nil, nil)
	if err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if got := cfg.Licensing().Source; got != "the control plane" {
		t.Errorf("license source = %q; a plane license must not be labeled a file key", got)
	}
}

// --license or HOOP_LICENSE outranks the plane, so a plane license that moves
// cannot be applied by a restart. noteLicense must not advise one.
func TestAMovedLicenseIsSilentWhenOverridden(t *testing.T) {
	rl, _ := testReloader(t, reloadBase)
	log, out := testLogger()
	cp := &controlPlane{url: "http://plane", token: "hsc_x", diskMode: true,
		fileDoc: []byte(reloadBase), planeLicense: "old", licenseOverridden: true}

	cp.handleAnswer(log, rl, []byte(`{"load_from_disk":true,"license":"new"}`), nil)
	if strings.Contains(out.String(), "the license the control plane sends changed") {
		t.Errorf("a moved license was advised even though a higher source wins; log:\n%s", out)
	}
	if cp.planeLicense != "new" {
		t.Errorf("planeLicense = %q; the move must still be tracked", cp.planeLicense)
	}
}
