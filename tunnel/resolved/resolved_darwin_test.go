//go:build darwin

package resolved

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withResolverDir points resolverDir at a t.TempDir() for the duration
// of a sub-test, restoring the production /etc/resolver path on cleanup.
func withResolverDir(t *testing.T) string {
	t.Helper()
	orig := resolverDir
	t.Cleanup(func() { resolverDir = orig })
	dir := filepath.Join(t.TempDir(), "resolver")
	resolverDir = dir
	return dir
}

// TestConfigure_WritesResolverFile asserts Configure creates
// <resolverDir>/<domain> with the expected nameserver + port lines.
func TestConfigure_WritesResolverFile(t *testing.T) {
	dir := withResolverDir(t)
	c := New()
	err := c.Configure(Config{
		Device:       "utun4",
		DNSAddress:   "fd00::1",
		SearchDomain: "hoop",
	})
	if err != nil {
		t.Fatalf("Configure: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "hoop"))
	if err != nil {
		t.Fatalf("read resolver file: %v", err)
	}
	s := string(body)
	if !strings.Contains(s, "nameserver fd00::1") {
		t.Errorf("missing nameserver line:\n%s", s)
	}
	if !strings.Contains(s, "port 53") {
		t.Errorf("missing port line:\n%s", s)
	}
}

// TestConfigure_Idempotent verifies a second Configure with the same
// inputs rewrites identical content (self-heal of a stale file).
func TestConfigure_Idempotent(t *testing.T) {
	dir := withResolverDir(t)
	c := New()
	cfg := Config{Device: "utun4", DNSAddress: "fd00::1", SearchDomain: "hoop"}
	if err := c.Configure(cfg); err != nil {
		t.Fatalf("Configure 1: %v", err)
	}
	first, _ := os.ReadFile(filepath.Join(dir, "hoop"))
	if err := c.Configure(cfg); err != nil {
		t.Fatalf("Configure 2: %v", err)
	}
	second, _ := os.ReadFile(filepath.Join(dir, "hoop"))
	if string(first) != string(second) {
		t.Errorf("Configure not idempotent:\n%q\nvs\n%q", first, second)
	}
}

// TestConfigure_ValidationRejectsEmpty ensures we never write a useless
// resolver file from obviously-broken inputs.
func TestConfigure_ValidationRejectsEmpty(t *testing.T) {
	dir := withResolverDir(t)
	c := New()
	cases := []Config{
		{Device: "", DNSAddress: "fd00::1", SearchDomain: "hoop"},
		{Device: "utun4", DNSAddress: "", SearchDomain: "hoop"},
		{Device: "utun4", DNSAddress: "fd00::1", SearchDomain: ""},
	}
	for i, cfg := range cases {
		if err := c.Configure(cfg); err == nil {
			t.Errorf("case %d: expected error for %+v", i, cfg)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("validation should not have written any file; found %d", len(entries))
	}
}

// TestUnconfigure_RemovesFile asserts the file written by Configure is
// removed by a subsequent Unconfigure.
func TestUnconfigure_RemovesFile(t *testing.T) {
	dir := withResolverDir(t)
	c := New()
	if err := c.Configure(Config{Device: "utun4", DNSAddress: "fd00::1", SearchDomain: "hoop"}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if err := c.Unconfigure("utun4"); err != nil {
		t.Fatalf("Unconfigure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hoop")); !os.IsNotExist(err) {
		t.Errorf("resolver file still present after Unconfigure (err=%v)", err)
	}
}

// TestUnconfigure_BeforeConfigureIsNoOp covers tearing down a tunnel
// that never successfully configured DNS — should not error.
func TestUnconfigure_BeforeConfigureIsNoOp(t *testing.T) {
	withResolverDir(t)
	c := New()
	if err := c.Unconfigure("utun4"); err != nil {
		t.Errorf("Unconfigure with no prior Configure should be nil, got %v", err)
	}
}

// TestCleanupStale_RemovesOrphanedFile exercises the crash-recovery
// path: a resolver file left behind by a SIGKILLed previous run is
// removed unconditionally at startup.
func TestCleanupStale_RemovesOrphanedFile(t *testing.T) {
	dir := withResolverDir(t)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	orphan := filepath.Join(dir, "hoop")
	if err := os.WriteFile(orphan, []byte("nameserver fd00::1\nport 53\n"), 0644); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	if err := CleanupStale("hoop"); err != nil {
		t.Fatalf("CleanupStale: %v", err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphaned resolver file not removed (err=%v)", err)
	}
}

// TestCleanupStale_MissingFileIsNoOp is the common (clean) case: no
// stale file means nothing to do.
func TestCleanupStale_MissingFileIsNoOp(t *testing.T) {
	withResolverDir(t)
	if err := CleanupStale("hoop"); err != nil {
		t.Errorf("CleanupStale with no file should be nil, got %v", err)
	}
}

// TestCleanupStale_EmptyDomainErrors guards the programming-error path.
func TestCleanupStale_EmptyDomainErrors(t *testing.T) {
	if err := CleanupStale(""); err == nil {
		t.Error("CleanupStale(\"\") should error")
	}
}
