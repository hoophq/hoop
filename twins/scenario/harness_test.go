//go:build scenario

// The scenario harness: it builds the real hoop binary, boots the compose
// stack (gateway + twins), and gives a scenario an HTTP client for the
// gateway API and the twins' admin APIs.
//
//	TWINS_REACH=alias|proxy|native
//	                          how the gateway reaches slack.com (default alias);
//	                          native is proxy with no Docker: every process on the host
//	TWINS_LICENSE_FILE=path   an enterprise license: review on exec needs one
//	TWINS_KEEP=1              leave the stack up after the run
//	TWINS_ARTIFACTS=dir       where the twin states and logs are written
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

const (
	apiURL    = "http://127.0.0.1:18009"
	oidcAdmin = "http://127.0.0.1:18081"
	slackAdm  = "http://127.0.0.1:18082"
	routerAdm = "http://127.0.0.1:18083"
)

type harness struct {
	t         *testing.T
	reach     string
	grpcURL   string
	root      string // repo root
	cli       string // hoop binary for this host, for `hoop exec`
	artifacts string
	marks     []mark
	start     time.Time
}

type mark struct {
	name string
	at   time.Duration
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, reach: envOr("TWINS_REACH", "alias"), root: root, start: time.Now(),
		grpcURL: "grpc://127.0.0.1:18010"}
	switch h.reach {
	case "alias", "proxy":
	case "native":
		// gRPC always listens on 8010; no container maps it elsewhere.
		h.grpcURL = "grpc://127.0.0.1:8010"
	default:
		t.Fatalf("TWINS_REACH must be alias, proxy or native, got %q", h.reach)
	}
	h.artifacts = envOr("TWINS_ARTIFACTS", t.TempDir())
	if err := os.MkdirAll(h.artifacts, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Logf("reach=%s artifacts=%s", h.reach, h.artifacts)
	return h
}

// mark records how long the run took to reach a step.
func (h *harness) mark(name string) {
	d := time.Since(h.start).Round(10 * time.Millisecond)
	h.marks = append(h.marks, mark{name, d})
	h.t.Logf("[%8s] %s", d, name)
}

func (h *harness) timings() string {
	var b strings.Builder
	prev := time.Duration(0)
	for _, m := range h.marks {
		fmt.Fprintf(&b, "%-44s %8s  (+%s)\n", m.name, m.at, (m.at - prev).Round(10*time.Millisecond))
		prev = m.at
	}
	return b.String()
}

// buildBinaries builds hoop for linux (mounted into the gateway container)
// and for this host (the CLI). HOOP_BIN_DIR and HOOP_CLI skip either build.
func (h *harness) buildBinaries() {
	h.t.Helper()
	gatewayOS := "linux"
	if h.reach == "native" {
		gatewayOS = runtime.GOOS
	}
	binDir := os.Getenv("HOOP_BIN_DIR")
	if binDir == "" {
		binDir = filepath.Join(h.artifacts, gatewayOS)
		h.goBuild(filepath.Join(binDir, "hoop"), gatewayOS, runtime.GOARCH)
		os.Setenv("HOOP_BIN_DIR", binDir)
	}
	h.cli = os.Getenv("HOOP_CLI")
	if h.cli == "" {
		if runtime.GOOS == gatewayOS {
			h.cli = filepath.Join(binDir, "hoop")
		} else {
			h.cli = filepath.Join(h.artifacts, "host", "hoop")
			h.goBuild(h.cli, runtime.GOOS, runtime.GOARCH)
		}
	}
}

func (h *harness) goBuild(out, goos, goarch string) {
	h.t.Helper()
	cmd := exec.Command("go", "build", "-o", out, "./client")
	cmd.Dir = h.root
	// The twins module runs with GOWORK=off; hoop builds in the workspace.
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0", "GOWORK="+filepath.Join(h.root, "go.work"))
	if b, err := cmd.CombinedOutput(); err != nil {
		h.t.Fatalf("go build hoop for %s/%s: %v\n%s", goos, goarch, err, b)
	}
}

func (h *harness) compose(args ...string) *exec.Cmd {
	files := []string{"-f", "compose.yml", "-f", "compose." + h.reach + ".yml"}
	cmd := exec.Command("docker", append(append([]string{"compose"}, files...), args...)...)
	cmd.Env = os.Environ()
	return cmd
}

func (h *harness) up() {
	h.t.Helper()
	if h.reach == "native" {
		h.upNative()
		return
	}
	_ = h.compose("down", "-v", "--remove-orphans").Run()
	if b, err := h.compose("up", "-d", "--build", "--wait").CombinedOutput(); err != nil {
		h.dumpLogs()
		h.t.Fatalf("compose up: %v\n%s", err, b)
	}
	h.t.Cleanup(func() {
		h.dumpLogs()
		if os.Getenv("TWINS_KEEP") == "" {
			_ = h.compose("down", "-v", "--remove-orphans").Run()
		}
	})
}

func (h *harness) dumpLogs() {
	if h.reach == "native" {
		return // each process logs to its own file
	}
	b, _ := h.compose("logs", "--no-color", "--timestamps").CombinedOutput()
	_ = os.WriteFile(filepath.Join(h.artifacts, "compose.log"), b, 0o644)
}

// call does one JSON request and decodes the answer into out (if not nil).
func (h *harness) call(method, url, token string, body, out any) int {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, rd)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		h.t.Logf("%s %s -> %d %s", method, url, resp.StatusCode, raw)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			h.t.Fatalf("%s %s: decode %q: %v", method, url, raw, err)
		}
	}
	return resp.StatusCode
}

// must is call that fails the scenario on a status outside 2xx.
func (h *harness) must(method, url, token string, body, out any) {
	h.t.Helper()
	if code := h.call(method, url, token, body, out); code >= 300 {
		h.t.Fatalf("%s %s: status %d", method, url, code)
	}
}

// eventually polls cond until it holds or the timeout passes.
func (h *harness) eventually(what string, timeout time.Duration, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out after %s waiting for %s", timeout, what)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// saveState writes a twin's /_twin/state to the artifacts and returns it.
func (h *harness) saveState(name, admin string) map[string]any {
	h.t.Helper()
	var st map[string]any
	h.must("GET", admin+"/_twin/state", "", nil, &st)
	b, _ := json.MarshalIndent(st, "", "  ")
	_ = os.WriteFile(filepath.Join(h.artifacts, name+"-state.json"), b, 0o644)
	return st
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
