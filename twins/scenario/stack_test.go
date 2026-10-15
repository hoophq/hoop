//go:build scenario

// Package scenario runs flows against the real hoop binary and the twins
// (ADR-0032). `make test-twins-scenario` runs it.
//
//	TWINS_REACH=native|proxy  native: every process runs on this host, Linux
//	                          only (darwin ignores SSL_CERT_FILE). proxy: the
//	                          same stack under docker compose. The default is
//	                          native on Linux and proxy elsewhere.
//	HOOP_BIN_DIR=dir          use the hoop binary in dir and build none (a
//	                          linux binary for proxy, a host one for native)
//	TWINS_ARTIFACTS=dir       write twin states, logs and timings to dir
//	TWINS_KEEP=1              leave the compose stack up after the run
package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Where a scenario reaches the stack, under both reaches.
const (
	apiURL      = "http://127.0.0.1:18009"
	oidcAdmin   = "http://127.0.0.1:18081"
	routerAdmin = "http://127.0.0.1:18083"

	// issuer is the OIDC twin as the gateway sees it. No DNS resolves the
	// host: the gateway reaches it through the router.
	issuer       = "https://oidc-twin"
	clientID     = "hoop"
	clientSecret = "hoop-secret"
)

// knownEgress is traffic the gateway sends to a real third party that has no
// twin. The router refuses it. The egress check fails on any other host.
var knownEgress = map[string]string{
	"api.honeycomb.io:443": "the tracing exporter starts in every build, with no API key too (common/monitoring NewOpenTracing)",
}

// egressSettle is how long the egress check lets background senders dial
// after the last scenario. The tracing exporter dials about 4s after the
// spans that feed it. A sender that dials later is not caught.
const egressSettle = 10 * time.Second

// stack is one gateway (`hoop start standalone`) with the twins and the
// router in front of it. A test function boots one and runs its scenarios
// on it, in order.
type stack struct {
	reach     string
	root      string // repo root
	artifacts string
	start     time.Time
	marks     []string

	// logins counts the users who logged in, so a scenario that needs an
	// empty org can refuse a used stack.
	logins int
	// admin is the token of the first user, who is admin.
	admin string
}

func newStack(t *testing.T) *stack {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	defaultReach := "proxy"
	if runtime.GOOS == "linux" {
		defaultReach = "native"
	}
	s := &stack{reach: envOr("TWINS_REACH", defaultReach), root: root, start: time.Now()}
	if s.reach != "native" && s.reach != "proxy" {
		t.Fatalf("TWINS_REACH must be native or proxy, got %q", s.reach)
	}
	if s.reach == "native" && runtime.GOOS != "linux" {
		t.Fatalf("TWINS_REACH=native needs Linux: %s ignores SSL_CERT_FILE, so the gateway cannot trust the twins. Use TWINS_REACH=proxy", runtime.GOOS)
	}
	s.artifacts = envOr("TWINS_ARTIFACTS", t.TempDir())
	if err := os.MkdirAll(s.artifacts, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Logf("reach=%s artifacts=%s", s.reach, s.artifacts)

	binDir := s.buildHoop(t)
	s.mark(t, "hoop built")
	if s.reach == "native" {
		s.upNative(t, binDir)
	} else {
		s.upCompose(t, binDir)
	}
	s.mark(t, "stack up: twins, router, hoop standalone healthy")
	// Registered after the stack, so it runs before the teardown.
	t.Cleanup(func() { s.finish(t) })
	return s
}

// mark logs how long the run took to reach a step.
func (s *stack) mark(t testing.TB, step string) {
	line := fmt.Sprintf("[%8s] %s", time.Since(s.start).Round(10*time.Millisecond), step)
	s.marks = append(s.marks, line)
	t.Log(line)
}

// buildHoop builds the hoop CLI from this branch for the OS the gateway runs
// on, unless HOOP_BIN_DIR names one.
func (s *stack) buildHoop(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv("HOOP_BIN_DIR"); dir != "" {
		if _, err := os.Stat(filepath.Join(dir, "hoop")); err != nil {
			t.Fatalf("HOOP_BIN_DIR: %v", err)
		}
		return dir
	}
	goos := runtime.GOOS
	if s.reach == "proxy" {
		goos = "linux"
	}
	dir := filepath.Join(s.artifacts, "bin-"+goos)
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "hoop"), "./client")
	cmd.Dir = s.root
	// The twins module runs with GOWORK=off; hoop builds in the workspace.
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0",
		"GOWORK="+filepath.Join(s.root, "go.work"))
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build hoop for %s/%s: %v\n%s", goos, runtime.GOARCH, err, b)
	}
	return dir
}

// finish runs once per stack, after its last scenario: it keeps the twin
// states, then fails on a host the gateway tried that no twin answers.
func (s *stack) finish(t *testing.T) {
	if !t.Failed() {
		time.Sleep(egressSettle)
	}
	s.saveState(t, "oidc", oidcAdmin)
	var st struct {
		State struct {
			Refused []struct {
				Host  string `json:"host"`
				Count int    `json:"count"`
			} `json:"refused"`
		} `json:"state"`
	}
	if raw := s.saveState(t, "router", routerAdmin); raw != nil {
		if err := json.Unmarshal(raw, &st); err != nil {
			t.Errorf("router state: %v", err)
		}
	}
	for _, r := range st.State.Refused {
		if why, ok := knownEgress[r.Host]; ok {
			t.Logf("egress refused %dx to %s: %s", r.Count, r.Host, why)
			continue
		}
		t.Errorf("the gateway tried %s %d times and no twin answers it: add a twin or a knownEgress entry with the reason (router-state.json)", r.Host, r.Count)
	}
	_ = os.WriteFile(filepath.Join(s.artifacts, "timings.txt"), []byte(strings.Join(s.marks, "\n")+"\n"), 0o644)
}

// saveState writes a twin's /_twin/state to the artifacts and returns it.
func (s *stack) saveState(t *testing.T, name, admin string) []byte {
	resp, err := http.Get(admin + "/_twin/state")
	if err != nil {
		t.Errorf("%s state: %v", name, err)
		return nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var pretty bytes.Buffer
	if json.Indent(&pretty, raw, "", "  ") == nil {
		raw = pretty.Bytes()
	}
	_ = os.WriteFile(filepath.Join(s.artifacts, name+"-state.json"), raw, 0o644)
	return raw
}

// call does one JSON request with no redirect and decodes the answer into
// out (when not nil). It returns the response, body already read.
func call(t testing.TB, method, target, token string, body, out any) *http.Response {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, target, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return resp
	}
	if resp.StatusCode >= 400 {
		t.Logf("%s %s -> %d %s", method, target, resp.StatusCode, raw)
	}
	if out != nil && len(raw) > 0 && resp.StatusCode < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s %s: decode %q: %v", method, target, raw, err)
		}
	}
	return resp
}

// must is call that fails the scenario on a status outside 2xx.
func must(t testing.TB, method, target, token string, body, out any) *http.Response {
	t.Helper()
	resp := call(t, method, target, token, body, out)
	if resp.StatusCode >= 300 {
		t.Fatalf("%s %s: status %d", method, target, resp.StatusCode)
	}
	return resp
}

// eventually polls cond until it holds or the timeout passes.
func eventually(t testing.TB, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, what)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

var noRedirect = &http.Client{
	Timeout:       30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func tail(b []byte, n int) string {
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}
