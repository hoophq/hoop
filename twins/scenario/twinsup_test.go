//go:build scenario && twinsup

package scenario

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// upUsers are the users `make twins-up` seeds, next to alice, the first
// user, whom the stack seeds and logs in at boot.
var upUsers = []map[string]any{
	{"sub": "bob-sub", "email": "bob@example.com", "name": "Bob", "groups": []string{"admin", "sre"}},
	{"sub": "carol-sub", "email": "carol@example.com", "name": "Carol", "groups": []string{"sre"}},
}

// TestUp is `make twins-up`: it stops the stack a previous run left, boots
// one with the web UI, seeds the users and leaves it up.
func TestUp(t *testing.T) {
	down(t)
	s := newStack(t, stackOpts{ui: true, up: true})
	seedOIDC(t, map[string]any{"users": upUsers})
	logs := "docker compose -p twins logs -f"
	if s.reach == "native" {
		logs = filepath.Join(s.artifacts, "native", "*.log")
	}
	fmt.Printf(`
The twins stack is up (reach=%s). Logs: %s
  hoop web UI        %s
  OIDC twin page     %s/_twin/ui
  router page        %s/_twin/ui
Log in as alice@example.com (admin, the first user), bob@example.com (group admin) or carol@example.com.
Stop it with: make twins-down

`, s.reach, logs, apiURL, oidcAdmin, routerAdmin)
}

// TestDown is `make twins-down`.
func TestDown(t *testing.T) { down(t) }

// down stops a stack that twins-up or TWINS_KEEP=1 left up.
func down(t *testing.T) {
	t.Helper()
	if stackReach(t) == "proxy" {
		// compose checks HOOP_BIN_DIR before `down` too.
		if b, err := composeCmd(os.TempDir(), "", "down", "-v", "--remove-orphans").CombinedOutput(); err != nil {
			t.Fatalf("compose down: %v\n%s", err, b)
		}
		return
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(envOr("TWINS_ARTIFACTS", upArtifacts(root)), "native")
	pids, _ := filepath.Glob(filepath.Join(dir, "*.pid"))
	for _, f := range pids {
		stopKept(t, f)
	}
}

// stopKept stops the process group a pid file names, when the process
// still runs the binary the file names: a stale pid can name an unrelated
// process.
func stopKept(t *testing.T, pidFile string) {
	t.Helper()
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pidText, bin, _ := strings.Cut(strings.TrimSpace(string(raw)), "\n")
	pid, err := strconv.Atoi(pidText)
	if err != nil || bin == "" {
		t.Fatalf("%s: want the pid and the binary, got %q", pidFile, raw)
	}
	defer os.Remove(pidFile)
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil || !bytes.HasPrefix(cmdline, []byte(bin+"\x00")) {
		t.Logf("%s: pid %d is gone or no longer runs %s", filepath.Base(pidFile), pid, bin)
		return
	}
	// spawn put it in a process group of its own.
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	deadline := time.Now().Add(10 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("stopped %s (pid %d)", strings.TrimSuffix(filepath.Base(pidFile), ".pid"), pid)
}
