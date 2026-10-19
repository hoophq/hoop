//go:build scenario

package scenario

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// upNative runs the twins, the router and `hoop start standalone` as
// processes on this host. Only the router reaches a twin's hostname, so
// nothing needs DNS. Linux only: darwin ignores SSL_CERT_FILE. uiDir, when
// set, is the web UI the gateway serves.
func (s *stack) upNative(t *testing.T, binDir, uiDir string) {
	t.Helper()
	// A fresh dir each run: a kept PGlite home would hold the last run's
	// users, and a kept test CA expires after a day.
	dir := filepath.Join(s.artifacts, "native")
	// The pid files are the only way twins-down finds a kept stack.
	if pids, _ := filepath.Glob(filepath.Join(dir, "*.pid")); len(pids) > 0 {
		t.Fatalf("a kept stack may still run from %s (%d pid files): run make twins-down first", dir, len(pids))
	}
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

	s.spawn(t, dir, "oidc-twin", nil, twins, "oidc", "-issuer="+issuer, "-browser-url="+oidcBrowser,
		"-tls=127.0.0.1:18441", "-admin=127.0.0.1:18081", "-cert="+pki+"/oidc.crt", "-key="+pki+"/oidc.key")
	s.spawn(t, dir, "router", nil, twins, "router", "-listen=127.0.0.1:18083", "-route=oidc-twin=127.0.0.1:18441")
	// The gateway loads the IdP discovery document at boot and stops if it
	// fails, so the twins come first.
	for _, u := range []string{oidcAdmin, routerAdmin} {
		waitOK(t, u+"/_twin/healthz", 15*time.Second, nil)
	}

	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	env := gatewayEnv(home, pki+"/ca.crt", "http://127.0.0.1:18083")
	if uiDir != "" {
		env = append(env, "STATIC_UI_PATH="+uiDir)
	}
	exited := s.spawn(t, dir, "gateway", env, filepath.Join(binDir, "hoop"), "start", "standalone")
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
//
// A kept stack leaves the process up, in a process group of its own, and
// writes its pid to <dir>/<name>.pid for `make twins-down`.
func (s *stack) spawn(t *testing.T, dir, name string, env []string, bin string, args ...string) <-chan struct{} {
	t.Helper()
	logPath := filepath.Join(dir, name+".log")
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = f, f
	if s.keep {
		// A Ctrl-C in the terminal of `make twins-up` must not reach it.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	if s.keep {
		// The binary lets twins-down tell this process from a later one
		// that reuses the pid.
		pid := []byte(strconv.Itoa(cmd.Process.Pid) + "\n" + bin + "\n")
		if err := os.WriteFile(filepath.Join(dir, name+".pid"), pid, 0o644); err != nil {
			_ = cmd.Process.Kill()
			t.Fatalf("%s: write its pid file: %v", name, err)
		}
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		if s.keep {
			f.Close()
			return
		}
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
// uiDir, when set, is the web UI the gateway serves.
func (s *stack) upCompose(t *testing.T, binDir, uiDir string) {
	t.Helper()
	compose := func(args ...string) *exec.Cmd { return composeCmd(binDir, uiDir, args...) }
	logs := func() {
		b, _ := compose("logs", "--no-color", "--timestamps").CombinedOutput()
		_ = os.WriteFile(filepath.Join(s.artifacts, "compose.log"), b, 0o644)
		if t.Failed() {
			t.Logf("last lines of compose.log:\n%s", tail(b, 40))
		}
	}
	_ = compose("down", "-v", "--remove-orphans").Run()
	// Registered before `up`: a failed build or health check still leaves
	// containers behind, and their ports would block the next run.
	t.Cleanup(func() {
		logs()
		if !s.keep {
			_ = compose("down", "-v", "--remove-orphans").Run()
		}
	})
	if b, err := compose("up", "-d", "--build", "--wait").CombinedOutput(); err != nil {
		t.Fatalf("compose up: %v\n%s", err, b)
	}
}

// composeCmd runs docker compose on the stack's files. compose.ui.yml adds
// the web UI when uiDir is set. Both variables must name a dir even for
// `down`, because compose checks them first.
func composeCmd(binDir, uiDir string, args ...string) *exec.Cmd {
	files := []string{"compose", "-f", "compose.yml"}
	if uiDir != "" {
		files = append(files, "-f", "compose.ui.yml")
	}
	cmd := exec.Command("docker", append(files, args...)...)
	cmd.Env = append(os.Environ(), "HOOP_BIN_DIR="+binDir, "HOOP_UI_DIR="+uiDir)
	return cmd
}
