package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)


func TestUserLaunchAgentPath(t *testing.T) {
	restore, fakeHome := withTempHome(t)
	defer restore()

	got, err := userLaunchAgentPath(Options{ServiceName: "hoop-agent"})
	if err != nil {
		t.Fatalf("userLaunchAgentPath returned error: %v", err)
	}

	want := filepath.Join(fakeHome, "Library", "LaunchAgents", "hoop-agent.plist")
	if got != want {
		t.Fatalf("userLaunchAgentPath = %q; want %q", got, want)
	}
}

func TestCurrentGuiTarget(t *testing.T) {
	uid := os.Getuid()
	got := currentGuiTarget()
	want := fmt.Sprintf("gui/%d", uid)

	if got != want {
		t.Fatalf("currentGuiTarget = %q; want %q", got, want)
	}

	if !strings.HasPrefix(got, "gui/") {
		t.Fatalf("currentGuiTarget should start with 'gui/': %q", got)
	}
}

func TestEnsureFile_CreatesAndIdempotent(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "subdir", "logs", "file.log")

	if err := ensureFile(target); err != nil {
		t.Fatalf("ensureFile(create) error: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("file not created: %v", err)
	}

	const content = "hello"
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatalf("write content: %v", err)
	}

	if err := ensureFile(target); err != nil {
		t.Fatalf("ensureFile(idempotent) error: %v", err)
	}
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after ensureFile: %v", err)
	}
	if string(b) != content {
		t.Fatalf("file content changed by ensureFile: got %q, want %q", string(b), content)
	}

	// 4) empty path should be a no-op
	if err := ensureFile(""); err != nil {
		t.Fatalf("ensureFile(\"\") = %v; want nil", err)
	}
}

func TestFixedDarwinLogPaths(t *testing.T) {
	restore, fakeHome := withTempHome(t)
	defer restore()

	stdout, stderr, err := fixedDarwinLogPaths("hoop-agent")
	if err != nil {
		t.Fatalf("fixedDarwinLogPaths returned error: %v", err)
	}

	wantOut := filepath.Join(fakeHome, "Library", "Logs", "hoop-agent.out.log")
	wantErr := filepath.Join(fakeHome, "Library", "Logs", "hoop-agent.err.log")

	if stdout != wantOut {
		t.Fatalf("stdout path = %q; want %q", stdout, wantOut)
	}
	if stderr != wantErr {
		t.Fatalf("stderr path = %q; want %q", stderr, wantErr)
	}
}
