package upgrade

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectShell(t *testing.T) {
	cases := map[string]ShellKind{
		"/bin/zsh":            ShellZsh,
		"/usr/local/bin/bash": ShellBash,
		"/opt/homebrew/bin/fish": ShellFish,
		"":                ShellUnknown,
		"/usr/bin/dash":   ShellUnknown,
		"/bin/zsh-5.9":    ShellUnknown,
	}
	for in, want := range cases {
		got := DetectShell(func(k string) string {
			if k == "SHELL" {
				return in
			}
			return ""
		})
		if got != want {
			t.Fatalf("DetectShell(%q): want %q got %q", in, want, got)
		}
	}
}

func TestRCFileFor(t *testing.T) {
	home := t.TempDir()
	if got := RCFileFor(ShellZsh, home); got != filepath.Join(home, ".zshrc") {
		t.Fatalf("zsh: got %s", got)
	}
	// bash should fall back to .bash_profile when .bashrc doesn't exist.
	if got := RCFileFor(ShellBash, home); got != filepath.Join(home, ".bash_profile") {
		t.Fatalf("bash fallback: got %s", got)
	}
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte(""), 0600); err != nil {
		t.Fatalf("write .bashrc: %v", err)
	}
	if got := RCFileFor(ShellBash, home); got != filepath.Join(home, ".bashrc") {
		t.Fatalf("bash with .bashrc: got %s", got)
	}
	if got := RCFileFor(ShellFish, home); got != filepath.Join(home, ".config", "fish", "config.fish") {
		t.Fatalf("fish: got %s", got)
	}
	if got := RCFileFor(ShellUnknown, home); got != "" {
		t.Fatalf("unknown: got %s", got)
	}
}

func TestPathExportLine(t *testing.T) {
	if l := PathExportLine(ShellZsh); !strings.Contains(l, `export PATH=`) || !strings.Contains(l, `.hoop/bin`) {
		t.Fatalf("zsh line: %s", l)
	}
	if l := PathExportLine(ShellFish); !strings.Contains(l, `fish_add_path`) {
		t.Fatalf("fish line: %s", l)
	}
	if PathExportLine(ShellUnknown) != "" {
		t.Fatalf("unknown should be empty")
	}
}

func TestIsPathConfigured(t *testing.T) {
	home := "/Users/test"
	hoopBin := home + "/.hoop/bin"

	if !IsPathConfigured(hoopBin+":/usr/bin", home) {
		t.Fatalf("absolute path should match")
	}
	if !IsPathConfigured("$HOME/.hoop/bin:/usr/bin", home) {
		t.Fatalf("$HOME expansion should match")
	}
	if !IsPathConfigured("~/.hoop/bin:/usr/bin", home) {
		t.Fatalf("tilde expansion should match")
	}
	if IsPathConfigured("/usr/bin:/bin", home) {
		t.Fatalf("non-matching PATH should return false")
	}
}

func TestAppendIfMissingIsIdempotent(t *testing.T) {
	home := t.TempDir()
	rc := filepath.Join(home, ".zshrc")
	line := PathExportLine(ShellZsh)

	added, err := AppendIfMissing(rc, line)
	if err != nil {
		t.Fatalf("AppendIfMissing: %v", err)
	}
	if !added {
		t.Fatalf("first call should append")
	}

	added2, err := AppendIfMissing(rc, line)
	if err != nil {
		t.Fatalf("AppendIfMissing 2: %v", err)
	}
	if added2 {
		t.Fatalf("second call should be no-op")
	}

	content, err := os.ReadFile(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := strings.Count(string(content), line); got != 1 {
		t.Fatalf("expected exactly 1 occurrence of line, got %d, content=%q", got, content)
	}
}

func TestAppendIfMissingCreatesFishConfigDir(t *testing.T) {
	home := t.TempDir()
	rc := RCFileFor(ShellFish, home)
	added, err := AppendIfMissing(rc, PathExportLine(ShellFish))
	if err != nil {
		t.Fatalf("AppendIfMissing: %v", err)
	}
	if !added {
		t.Fatalf("expected append")
	}
	if _, err := os.Stat(rc); err != nil {
		t.Fatalf("config file should exist: %v", err)
	}
}
