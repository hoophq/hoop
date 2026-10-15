//go:build scenario

package scenario

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// upNative runs the twins, the router and `hoop start standalone` as
// processes on this host. Only the router reaches a twin's hostname, so
// nothing needs DNS. Linux only: darwin ignores SSL_CERT_FILE.
func (s *stack) upNative(t *testing.T, binDir string) {
	t.Helper()
	// A fresh dir each run: a kept PGlite home would hold the last run's
	// users, and a kept test CA expires after a day.
	dir := filepath.Join(s.artifacts, "native")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	twins := filepath.Join(dir, "twins")
	build := exec.Command("go", "build", "-o", twins, "./cmd/twins")
	build.Dir = ".."
	build.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build twins: %v\n%s", err, b)
	}
	pki := filepath.Join(dir, "pki")
	if b, err := exec.Command(twins, "pki", "-dir="+pki, "-leaf=oidc=oidc-twin,localhost,127.0.0.1").CombinedOutput(); err != nil {
		t.Fatalf("twins pki: %v\n%s", err, b)
	}

	spawn(t, dir, "oidc-twin", nil, twins, "oidc", "-issuer="+issuer,
		"-tls=127.0.0.1:18441", "-admin=127.0.0.1:18081", "-cert="+pki+"/oidc.crt", "-key="+pki+"/oidc.key")
	spawn(t, dir, "router", nil, twins, "router", "-listen=127.0.0.1:18083", "-route=oidc-twin=127.0.0.1:18441")
	// The gateway loads the IdP discovery document at boot and stops if it
	// fails, so the twins come first.
	for _, u := range []string{oidcAdmin, routerAdmin} {
		waitOK(t, u+"/_twin/healthz", 15*time.Second, nil)
	}

	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	exited := spawn(t, dir, "gateway", gatewayEnv(home, pki+"/ca.crt", "http://127.0.0.1:18083"),
		filepath.Join(binDir, "hoop"), "start", "standalone")
	waitOK(t, apiURL+"/api/healthz", 120*time.Second, exited)
}

// gatewayEnv is the whole environment of the gateway process. It inherits
// only PATH, so a developer's POSTGRES_DB_URI or IDP_* cannot leak in.
func gatewayEnv(home, caFile, proxy string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + home, "TMPDIR=" + os.TempDir(),
		// The API listens on PORT; gRPC always listens on 8010.
		"PORT=18009", "API_URL=" + apiURL, "GRPC_URL=grpc://127.0.0.1:8010",
		"IDP_ISSUER=" + issuer, "IDP_CLIENT_ID=" + clientID, "IDP_CLIENT_SECRET=" + clientSecret,
		// The test CA replaces the system roots: no real service can answer.
		"SSL_CERT_FILE=" + caFile, "HTTPS_PROXY=" + proxy,
		"DO_NOT_TRACK=1", "GIN_MODE=release", "LOG_ENCODING=console",
	}
}

// spawn runs a long-lived process with its output in <dir>/<name>.log, and
// stops it when the test ends. env nil inherits this process's environment.
// The channel closes when the process exits.
func spawn(t *testing.T, dir, name string, env []string, bin string, args ...string) <-chan struct{} {
	t.Helper()
	logPath := filepath.Join(dir, name+".log")
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = f, f
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		f.Close()
		if t.Failed() {
			b, _ := os.ReadFile(logPath)
			t.Logf("last lines of %s:\n%s", logPath, tail(b, 30))
		}
	})
	return done
}

// waitOK polls url until it answers 200. It fails at once when exited
// closes: the process it waits for is gone.
func waitOK(t *testing.T, url string, timeout time.Duration, exited <-chan struct{}) {
	t.Helper()
	eventually(t, url, timeout, func() bool {
		select {
		case <-exited:
			t.Fatalf("the process behind %s exited before it answered", url)
		default:
		}
		resp, err := http.Get(url)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
}

// upCompose runs the same stack under docker compose, for a laptop. The
// hoop binary is a linux build from this branch, mounted into the gateway.
func (s *stack) upCompose(t *testing.T, binDir string) {
	t.Helper()
	compose := func(args ...string) *exec.Cmd {
		cmd := exec.Command("docker", append([]string{"compose", "-f", "compose.yml"}, args...)...)
		cmd.Env = append(os.Environ(), "HOOP_BIN_DIR="+binDir)
		return cmd
	}
	logs := func() {
		b, _ := compose("logs", "--no-color", "--timestamps").CombinedOutput()
		_ = os.WriteFile(filepath.Join(s.artifacts, "compose.log"), b, 0o644)
		if t.Failed() {
			t.Logf("last lines of compose.log:\n%s", tail(b, 40))
		}
	}
	_ = compose("down", "-v", "--remove-orphans").Run()
	if b, err := compose("up", "-d", "--build", "--wait").CombinedOutput(); err != nil {
		logs()
		t.Fatalf("compose up: %v\n%s", err, b)
	}
	t.Cleanup(func() {
		logs()
		if os.Getenv("TWINS_KEEP") == "" {
			_ = compose("down", "-v", "--remove-orphans").Run()
		}
	})
}
