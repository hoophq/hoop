//go:build scenario

package scenario

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// upNative is option B with no Docker: the twins, the router and
// `hoop start standalone` run as host processes. The router is the only way
// to the twins' hostnames, so nothing needs DNS. It works on Linux only:
// darwin ignores SSL_CERT_FILE, so the gateway cannot trust the test CA.
func (h *harness) upNative() {
	h.t.Helper()
	dir := filepath.Join(h.artifacts, "native")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		h.t.Fatal(err)
	}
	twins := filepath.Join(dir, "twins")
	build := exec.Command("go", "build", "-o", twins, "./cmd/twins")
	build.Dir = ".."
	build.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	if b, err := build.CombinedOutput(); err != nil {
		h.t.Fatalf("go build twins: %v\n%s", err, b)
	}
	pki := filepath.Join(dir, "pki")
	h.run(dir, "pki", twins, "pki", "-dir="+pki,
		"-leaf=oidc=oidc-twin,localhost,127.0.0.1",
		"-leaf=slack=slack.com,wss-primary.slack.com,hooks.slack.com,localhost,127.0.0.1").Wait()

	h.spawn(dir, "oidc-twin", nil, twins, "oidc", "-issuer=https://oidc-twin",
		"-tls=127.0.0.1:18441", "-admin=127.0.0.1:18081", "-cert="+pki+"/oidc.crt", "-key="+pki+"/oidc.key")
	h.spawn(dir, "slack-twin", nil, twins, "slack",
		"-tls=127.0.0.1:18442", "-admin=127.0.0.1:18082", "-cert="+pki+"/slack.crt", "-key="+pki+"/slack.key")
	h.spawn(dir, "router", nil, twins, "router", "-listen=127.0.0.1:18083",
		"-route=oidc-twin=127.0.0.1:18441", "-route=slack.com=127.0.0.1:18442",
		"-route=wss-primary.slack.com=127.0.0.1:18442", "-route=hooks.slack.com=127.0.0.1:18442")
	for _, u := range []string{oidcAdmin, slackAdm, routerAdm} {
		h.waitHealthy(u+"/_twin/healthz", 15*time.Second)
	}

	home := filepath.Join(dir, "home")
	_ = os.MkdirAll(home, 0o755)
	gw := h.spawn(dir, "gateway", []string{
		"HOME=" + home, "PORT=18009", "API_URL=" + apiURL, "GRPC_URL=grpc://127.0.0.1:8010",
		"IDP_ISSUER=https://oidc-twin", "IDP_CLIENT_ID=hoop", "IDP_CLIENT_SECRET=hoop-secret",
		"SSL_CERT_FILE=" + pki + "/ca.crt", "HTTPS_PROXY=http://127.0.0.1:18083",
		"DO_NOT_TRACK=1", "GIN_MODE=release", "LOG_ENCODING=console",
	}, filepath.Join(os.Getenv("HOOP_BIN_DIR"), "hoop"), "start", "standalone")
	h.eventually("hoop standalone to be healthy", 120*time.Second, func() bool {
		select {
		case <-gw:
			log, _ := os.ReadFile(filepath.Join(dir, "gateway.log"))
			h.t.Fatalf("hoop standalone exited before it was healthy:\n%s", tail(log, 15))
		default:
		}
		resp, err := http.Get(apiURL + "/api/healthz")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
}

func tail(b []byte, n int) string {
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (h *harness) run(dir, name, bin string, args ...string) *exec.Cmd {
	cmd := exec.Command(bin, args...)
	f, err := os.Create(filepath.Join(dir, name+".log"))
	if err != nil {
		h.t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = f, f
	if err := cmd.Start(); err != nil {
		h.t.Fatalf("start %s: %v", name, err)
	}
	return cmd
}

// spawn runs a long-lived process and stops it when the test ends. The
// channel closes when the process exits.
func (h *harness) spawn(dir, name string, env []string, bin string, args ...string) <-chan struct{} {
	h.t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	f, err := os.Create(filepath.Join(dir, name+".log"))
	if err != nil {
		h.t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = f, f
	if err := cmd.Start(); err != nil {
		h.t.Fatalf("start %s: %v", name, err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	h.t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
		}
		f.Close()
	})
	return done
}

func (h *harness) waitHealthy(url string, timeout time.Duration) {
	h.t.Helper()
	h.eventually(url, timeout, func() bool {
		resp, err := http.Get(url)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
}
