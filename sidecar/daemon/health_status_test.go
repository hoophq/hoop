package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/license/licensetest"
)

// servingHealth is the status of a process whose Run published a registry
// holding lanes.
func servingHealth(lanes ...runningLane) *healthStatus {
	return servingHealthICA(false, &laneSet{lanes: lanes})
}

// servingHealthICA publishes set with admin.ica_readiness_json as ica says.
func servingHealthICA(ica bool, set *laneSet) *healthStatus {
	h := newHealthStatus()
	h.serving(set, ica, func() license.Status { return license.Status{} })
	return h
}

// endpointLane is a registry entry for an endpoint lane.
func endpointLane(name, protocol string, srv endpointServer) runningLane {
	return runningLane{name: name, protocol: protocol, endpoint: srv}
}

// readyz calls /readyz on h and decodes the body.
func readyz(t *testing.T, h *healthStatus) (int, readiness, string) {
	t.Helper()
	mux := http.NewServeMux()
	handleHealth(mux, h)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var r readiness
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatalf("/readyz body %q is not the status JSON: %v", w.Body, err)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	return w.Code, r, w.Body.String()
}

func healthz(t *testing.T, h *healthStatus) int {
	t.Helper()
	mux := http.NewServeMux()
	handleHealth(mux, h)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	return w.Code
}

// ENG-1116: before the first config arrives, a plane-connected sidecar is
// alive and not ready, and says it waits for its control plane.
func TestReadyzBeforeTheConfigWaitsForThePlane(t *testing.T) {
	h := newHealthStatus()
	h.planeConfigured()
	h.bootFailed(planeUnavailableError{status: 503, err: errors.New("x")})
	h.bootFailed(planeUnavailableError{status: 503, err: errors.New("x")})

	if code := healthz(t, h); code != http.StatusOK {
		t.Errorf("/healthz during the boot retry = %d, want 200", code)
	}
	code, r, _ := readyz(t, h)
	if code != http.StatusServiceUnavailable || r.Status != "not_ready" || r.Reason != readyWaitingForPlane {
		t.Errorf("/readyz = %d %+v, want 503 waiting_for_control_plane", code, r)
	}
	if !r.ProcessRunning || r.PID == 0 || r.ConfigLoaded || r.LanesOpen || !r.ControlPlaneEnabled ||
		r.ConnectedToControlPlane || r.ValidLicense {
		t.Errorf("/readyz fields = %+v", r)
	}
	if r.ControlPlane == nil || r.ControlPlane.Error != planeErrServer || r.ControlPlane.BootAttempts != 2 ||
		r.ControlPlane.LastHandshake != "" {
		t.Errorf("control_plane = %+v, want server_error after 2 attempts and no handshake", r.ControlPlane)
	}
	if r.Lanes == nil {
		t.Error("lanes is null, want [] so a script can iterate it")
	}

	// The plane answered; Run has not published the lanes yet.
	h.planeAnswered(time.Now())
	if _, r, _ := readyz(t, h); r.Reason != readyStarting || !r.ConnectedToControlPlane {
		t.Errorf("/readyz after the handshake = %+v, want starting and connected", r)
	}
}

// A standalone process has no plane to wait for: before Run, it is starting.
func TestReadyzStandaloneBeforeTheConfigIsStarting(t *testing.T) {
	code, r, _ := readyz(t, newHealthStatus())
	if code != http.StatusServiceUnavailable || r.Reason != readyStarting || r.ControlPlaneEnabled || r.ControlPlane != nil {
		t.Errorf("/readyz = %d %+v, want 503 starting, no control plane", code, r)
	}
}

// Lucas's decision on ENG-1116: once a config is loaded, a lost control plane
// never turns /readyz to 503. The lanes serve the last good config; the body
// says the plane is lost.
func TestReadyzStaysReadyWhenThePlaneIsLost(t *testing.T) {
	h := servingHealth(endpointLane("orders", "grpc", boundLane{addr: &net.TCPAddr{Port: 1}}))
	h.planeConfigured()
	answered := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	h.planeAnswered(answered)

	for _, tc := range []struct {
		err  error
		code string
	}{
		{planeUnavailableError{err: errors.New("dial tcp: connection refused")}, planeErrUnreachable},
		{planeUnavailableError{status: 502, err: errors.New("answered 502")}, planeErrServer},
		{fmt.Errorf("%w (at x)", ErrPlaneHasNoConfig), planeErrNoConfig},
		{errors.New("the control plane rejected the token"), planeErrRefused},
	} {
		h.planeFailed(tc.err)
		code, r, _ := readyz(t, h)
		if code != http.StatusOK || r.Status != "ready" || r.ConnectedToControlPlane || !r.ConfigLoaded || !r.LanesOpen {
			t.Errorf("%s: /readyz = %d %+v, want 200 ready, not connected", tc.code, code, r)
		}
		if r.ControlPlane == nil || r.ControlPlane.Error != tc.code || r.ControlPlane.LastHandshake != "2026-11-23T12:00:00Z" {
			t.Errorf("%s: control_plane = %+v, want the code and the last good handshake", tc.code, r.ControlPlane)
		}
	}
	h.planeAnswered(answered.Add(time.Minute))
	if _, r, _ := readyz(t, h); !r.ConnectedToControlPlane || r.ControlPlane.Error != "" {
		t.Errorf("after a good heartbeat = %+v, want connected with no error", r)
	}
}

// One aggregate endpoint: a lane that cannot serve makes the process not
// ready, and the body names it. The lane readiness that predates ENG-1116
// (an ICA engine loading, a gRPC lane without its schema) keeps its 503.
func TestReadyzNamesTheLaneThatIsNotOpen(t *testing.T) {
	loading := &loadingLane{}
	h := servingHealth(
		endpointLane("orders", "grpc", boundLane{addr: &net.TCPAddr{Port: 1}}),
		endpointLane("desktops", "ica", loading),
		endpointLane("ledger", "ssh", boundLane{}),
	)
	code, r, _ := readyz(t, h)
	if code != http.StatusServiceUnavailable || r.Reason != readyLanesNotOpen || r.LanesOpen || !r.ConfigLoaded {
		t.Errorf("/readyz = %d %+v, want 503 lanes_not_open", code, r)
	}
	want := []laneReadiness{
		{"orders", "grpc", laneOpen},
		{"desktops", "ica", laneNotReady},
		{"ledger", "ssh", laneStarting},
	}
	if fmt.Sprint(r.Lanes) != fmt.Sprint(want) {
		t.Errorf("lanes = %+v, want %+v", r.Lanes, want)
	}

	loading.ready.Store(true)
	h.lanes.lanes[2].endpoint = boundLane{addr: &net.TCPAddr{Port: 2}}
	if code, r, _ := readyz(t, h); code != http.StatusOK || !r.LanesOpen || r.Reason != "" {
		t.Errorf("/readyz once every lane serves = %d %+v, want 200", code, r)
	}
}

// The license in force is read on every call: a renewal or an expiry shows
// on the next probe, and never changes the status code.
func TestReadyzReportsTheLicenseInForce(t *testing.T) {
	var mu sync.Mutex
	lic := license.Status{}
	h := newHealthStatus()
	h.serving(&laneSet{}, false, func() license.Status { mu.Lock(); defer mu.Unlock(); return lic })
	if code, r, _ := readyz(t, h); code != http.StatusOK || r.ValidLicense {
		t.Errorf("no license: /readyz = %d %+v, want 200, valid_license false", code, r)
	}
	mu.Lock()
	lic = licensetest.Status(t, licensetest.Enterprise())
	mu.Unlock()
	if _, r, _ := readyz(t, h); !r.ValidLicense {
		t.Errorf("a valid license: valid_license = false")
	}
}

// The probes have no authentication. The body carries codes, never an
// error's text: that can hold the plane's URL, a token source, an upstream.
func TestReadyzLeaksNoErrorText(t *testing.T) {
	h := servingHealth()
	h.planeConfigured()
	secret := "https://cp.internal.example/hoop token hsc_SECRET upstream db.internal:5432"
	h.bootFailed(planeUnavailableError{err: errors.New(secret)})
	h.planeFailed(errors.New(secret))
	_, _, body := readyz(t, h)
	for _, leak := range []string{"cp.internal.example", "hsc_", "db.internal", "token"} {
		if strings.Contains(body, leak) {
			t.Errorf("/readyz body leaks %q: %s", leak, body)
		}
	}
}

// The ICA attestation (admin.ica_readiness_json) is what ICA deployments
// parse; the process status must not replace it.
func TestReadyzKeepsTheICAAttestation(t *testing.T) {
	ica := &fakeICAServer{ready: true}
	h := servingHealthICA(true, &laneSet{lanes: []runningLane{endpointLane("desktops", "ica", ica)}})
	mux := http.NewServeMux()
	handleHealth(mux, h)
	got := httptest.NewRecorder()
	mux.ServeHTTP(got, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	want := httptest.NewRecorder()
	writeICAReadiness(want, []endpointServer{ica}, 0)
	if got.Code != want.Code || got.Body.String() != want.Body.String() {
		t.Errorf("/readyz = %d %q, want the attestation %d %q", got.Code, got.Body, want.Code, want.Body)
	}
}

// ENG-1116 on top of ENG-1153: with HOOP_SIDECAR_HEALTH_LISTEN set, the
// listener answers from process start. While the boot retries a failing
// plane, /healthz is 200 and /readyz is 503 with the boot's progress; Setup
// hands Run the same status, so the listener reports the lanes once Run
// publishes them.
func TestTheEarlyHealthListenerAnswersDuringTheBootRetry(t *testing.T) {
	fastBootRetry(t, time.Minute)
	t.Setenv(HealthListenEnv, freeTCPAddr(t))
	var mu sync.Mutex
	down := true
	plane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if down {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(planeConfig))
	}))
	t.Cleanup(plane.Close)
	t.Setenv(ControlPlaneURLEnv, plane.URL)
	t.Setenv(SidecarTokenEnv, "hsc_env")

	hl, err := ListenHealth()
	if err != nil || hl == nil {
		t.Fatalf("ListenHealth = %v, %v", hl, err)
	}
	t.Cleanup(func() { _ = hl.Close() })
	base := "http://" + hl.Addr()

	type result struct {
		cfg *Config
		err error
	}
	done := make(chan result, 1)
	go func() {
		cfg, _, err := SetupWith("", nil, nil, WithHealthListener(hl))
		done <- result{cfg, err}
	}()

	var r readiness
	waitFor(t, func() bool {
		code, body := httpCall(t, http.MethodGet, base+"/readyz")
		_ = json.Unmarshal([]byte(body), &r)
		return code == http.StatusServiceUnavailable && r.ControlPlane != nil && r.ControlPlane.BootAttempts >= 2
	})
	if r.Reason != readyWaitingForPlane || r.ControlPlane.Error != planeErrServer || r.ConfigLoaded {
		t.Errorf("/readyz during the retry = %+v, want waiting_for_control_plane, server_error", r)
	}
	if code, _ := httpCall(t, http.MethodGet, base+"/healthz"); code != http.StatusOK {
		t.Errorf("/healthz during the retry = %d, want 200", code)
	}

	mu.Lock()
	down = false
	mu.Unlock()
	res := <-done
	if res.err != nil {
		t.Fatalf("SetupWith: %v", res.err)
	}
	if res.cfg.health != hl.status || res.cfg.healthListener != hl {
		t.Fatal("Setup did not hand Run the listener's status")
	}
	// What Run does once the lanes are built.
	res.cfg.health.serving(&laneSet{lanes: []runningLane{endpointLane("appdb", "postgres", boundLane{addr: &net.TCPAddr{Port: 1}})}},
		false, func() license.Status { return license.Status{} })
	code, body := httpCall(t, http.MethodGet, base+"/readyz")
	if err := json.Unmarshal([]byte(body), &r); err != nil || code != http.StatusOK || !r.ConnectedToControlPlane ||
		r.ControlPlane.LastHandshake == "" {
		t.Errorf("/readyz once serving = %d %s, want 200 connected", code, body)
	}
}

// A check does not bind the health port: -validate inherits the variable
// inside a pod whose sidecar already holds it.
func TestListenHealthIsOffWithoutTheVariable(t *testing.T) {
	t.Setenv(HealthListenEnv, "")
	if hl, err := ListenHealth(); hl != nil || err != nil {
		t.Fatalf("ListenHealth with no address = %v, %v; want nil, nil", hl, err)
	}
	if err := (*HealthListener)(nil).Close(); err != nil {
		t.Fatal(err)
	}
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	t.Setenv(HealthListenEnv, held.Addr().String())
	if _, err := ListenHealth(); err == nil || !strings.Contains(err.Error(), HealthListenEnv) {
		t.Fatalf("ListenHealth on a held port = %v, want an error naming %s", err, HealthListenEnv)
	}
}
