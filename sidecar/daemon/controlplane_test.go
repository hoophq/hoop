package daemon

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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

// planeConfigWith renders the plane's answer carrying a license document, the
// way the gateway serves the organization's license: inside the config
// document, in the key the schema already declares.
func planeConfigWith(t *testing.T, doc string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"listeners": []map[string]string{
			{"name": "appdb", "protocol": "postgres", "listen": ":1", "upstream": "h:5432"},
		},
		"license": doc,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// The point of the feature: an operator licenses a fleet in the control plane
// and no sidecar carries a license of its own.
func TestThePlaneLicensesASidecarThatCarriesNone(t *testing.T) {
	doc := licensetest.Document(t, licensetest.Enterprise())
	srv, _ := planeServer(t, http.StatusOK, planeConfigWith(t, doc))
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")

	cfg, _, err := SetupWith("", nil, nil)
	if err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if got := cfg.Licensing().State(); got != license.StateValid {
		t.Fatalf("license state = %q, want valid", got)
	}
	if got := cfg.Licensing().Source; got != PlaneLicenseSource {
		t.Errorf("license source = %q, want %q", got, PlaneLicenseSource)
	}
}

// The decision this implements: the control plane is the source of truth. A
// license set on the machine does not overrule the fleet's, whichever of the
// three local sources it came from.
func TestThePlaneLicenseOutranksEveryLocalSource(t *testing.T) {
	// Signed FIRST, so the trust root each Sign installs leaves these three
	// unverifiable: licensetest swaps it per call and the last one wins.
	// That is what makes the assertion sharp -- an invalid license stops
	// startup, so picking any local source here fails loudly rather than
	// quietly returning the wrong document.
	envDoc := licensetest.Document(t, licensetest.Expiring(-time.Hour))
	fileDoc := licensetest.Document(t, licensetest.Expiring(-2*time.Hour))
	flagDoc := licensetest.Document(t, licensetest.Expiring(-3*time.Hour))
	planeDoc := licensetest.Document(t, licensetest.Enterprise())

	srv, _ := planeServer(t, http.StatusOK, planeConfigWith(t, planeDoc))
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")
	t.Setenv(license.EnvVar, envDoc)

	body, err := json.Marshal(map[string]string{"license": fileDoc})
	if err != nil {
		t.Fatal(err)
	}

	cfg, _, err := SetupWith(writeConfig(t, string(body)), nil, nil, WithLicense(flagDoc))
	if err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if got := cfg.Licensing().State(); got != license.StateValid {
		t.Fatalf("license state = %q, want valid: a local source outranked the control plane", got)
	}
	if got := cfg.Licensing().Source; got != PlaneLicenseSource {
		t.Errorf("license source = %q, want %q", got, PlaneLicenseSource)
	}
}

// A plane whose organization has no license leaves the local sources alone.
// Standalone licensing keeps working while a fleet is only partly migrated.
func TestAPlaneWithoutALicenseLeavesTheLocalOnesInForce(t *testing.T) {
	srv, _ := planeServer(t, http.StatusOK, planeConfig)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")
	t.Setenv(license.EnvVar, licensetest.Document(t, licensetest.Enterprise()))

	cfg, _, err := SetupWith("", nil, nil)
	if err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if got := cfg.Licensing().State(); got != license.StateValid {
		t.Fatalf("license state = %q, want valid", got)
	}
	if got := cfg.Licensing().Source; got != license.EnvVar {
		t.Errorf("license source = %q, want %q", got, license.EnvVar)
	}
}

// The license the plane sends must never reach the document this process
// pushes back: the organization owns it, and a copy on the sidecar's row
// goes stale the day it is renewed.
func TestTheImportedDocumentCarriesNoLicense(t *testing.T) {
	var imported []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			imported, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(planeConfig))
			return
		}
		if imported == nil {
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = w.Write([]byte(`{"message":"no configuration is assigned"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(planeConfig))
	}))
	t.Cleanup(srv.Close)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")

	body, err := json.Marshal(map[string]any{
		"license": licensetest.Document(t, licensetest.Enterprise()),
		"listeners": []map[string]string{
			{"name": "appdb", "protocol": "postgres", "listen": ":1", "upstream": "h:5432"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := SetupWith(writeConfig(t, string(body)), nil, nil); err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if imported == nil {
		t.Fatal("the config file was never imported")
	}
	var pushed map[string]any
	if err := json.Unmarshal(imported, &pushed); err != nil {
		t.Fatalf("the imported document is not JSON: %v", err)
	}
	if _, ok := pushed["license"]; ok {
		t.Errorf("the imported document carries a license: %s", imported)
	}
}
