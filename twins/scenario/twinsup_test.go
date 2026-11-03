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

// TestUp is `make twins-up`: it boots the stack with the web UI, seeds the
// users and leaves it up. Like any run, it refuses beside a kept stack, so
// replacing one takes `make twins-down` first.
func TestUp(t *testing.T) {
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

// down stops a stack that twins-up or TWINS_KEEP=1 left up, of either
// reach: the kept stack's reach can differ from this run's TWINS_REACH.
func down(t *testing.T) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(envOr("TWINS_ARTIFACTS", upArtifacts(root)), "native")
	for _, d := range keptNativeDirs(t, root, dir) {
		for _, f := range pidFiles(t, d) {
			stopKept(t, f)
		}
	}
	// Last: a failed step above keeps the record of where the stack runs.
	if err := os.Remove(keptNativeFile(root)); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	// Only a kept compose stack: a scenario run on this host owns its
	// containers until it ends, and its next run takes down a crashed one.
	if len(composeContainers(t, "label=twins.kept=yes")) > 0 {
		// compose checks HOOP_BIN_DIR before `down` too.
		if b, err := composeCmd(os.TempDir(), "", false, "down", "-v", "--remove-orphans").CombinedOutput(); err != nil {
			t.Fatalf("compose down: %v\n%s", err, b)
		}
		t.Log("stopped the compose stack")
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
