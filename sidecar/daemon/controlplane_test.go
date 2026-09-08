package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/license/licensetest"
)

// planeConfig is what a healthy control plane answers: one postgres lane,
// the same shape the gateway's BuildSidecarConfig produces.
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

// The plane supplies the listeners, so a file that also declares them is two
// authorities for one fact. Refused for the same reason normalize refuses a
// field written in both spellings: nobody can predict the winner.
func TestAConfigFileWithListenersConflictsWithAControlPlane(t *testing.T) {
	srv, _ := planeServer(t, http.StatusOK, planeConfig)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")

	_, _, err := SetupWith(writeConfig(t, minimalConfig+`}`), nil, nil)
	if err == nil {
		t.Fatal("a file with listeners was accepted beside a control plane")
	}
	if !strings.Contains(err.Error(), "listener") {
		t.Errorf("the error does not say what conflicts: %v", err)
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

// Validate relaxes the listener check whenever a plane is configured, so the
// explicit check on the FETCHED config is what stands between an empty
// answer and a process serving nothing.
func TestAnEmptyPlaneConfigIsRefused(t *testing.T) {
	srv, _ := planeServer(t, http.StatusOK, `{"listeners":[]}`)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")

	_, _, err := SetupWith("", nil, nil)
	if err == nil {
		t.Fatal("a config with no listeners was accepted from the plane")
	}
	if !strings.Contains(err.Error(), "no listeners") {
		t.Errorf("the error does not say what is missing: %v", err)
	}
}

// One edit on the plane logs once, not once per tick: lastRaw advances when
// a change is seen.
func TestPollNoticesAChangeOnce(t *testing.T) {
	srv, _ := planeServer(t, http.StatusOK, planeConfig)
	cp := &controlPlane{url: srv.URL, token: "hsc_x", lastRaw: []byte(`{"old":true}`)}

	changed, err := cp.poll()
	if err != nil || !changed {
		t.Fatalf("first poll = %v, %v; want a change", changed, err)
	}
	changed, err = cp.poll()
	if err != nil || changed {
		t.Fatalf("second poll = %v, %v; want no change", changed, err)
	}
}
