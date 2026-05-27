//go:build linux

package resolved

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeResolvectl is a tiny shell script that records its invocation
// args to a file and exits with the requested status. Lets us drive
// the Configurer end-to-end without ever talking to a real
// systemd-resolved.
//
// We do NOT mock the exec.Command call itself (no monkey patching
// of os/exec). Driving a real fork+exec of a real script tests the
// argument quoting + the "binary on PATH" behaviour end-to-end.
func fakeResolvectl(t *testing.T, exitCode int, output string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "resolvectl")
	body := "#!/bin/sh\n"
	if output != "" {
		body += "echo " + escapeShell(output) + "\n"
	}
	// Log every invocation so test assertions can read the args.
	body += `echo "$@" >> "` + dir + `/calls"` + "\n"
	body += "exit " + itoa(exitCode) + "\n"
	if err := os.WriteFile(bin, []byte(body), 0755); err != nil {
		t.Fatalf("write fake resolvectl: %v", err)
	}
	return bin
}

func readCalls(t *testing.T, bin string) []string {
	t.Helper()
	logPath := filepath.Join(filepath.Dir(bin), "calls")
	body, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read calls: %v", err)
	}
	out := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

// withRuntimeDir gives us a fake /run/systemd/resolve/ for the
// duration of a sub-test, restoring the production path on cleanup.
func withRuntimeDir(t *testing.T, present bool) {
	t.Helper()
	orig := runtimeDir
	t.Cleanup(func() { runtimeDir = orig })
	if !present {
		runtimeDir = filepath.Join(t.TempDir(), "no-such-dir")
		return
	}
	dir := filepath.Join(t.TempDir(), "resolved")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir runtimeDir: %v", err)
	}
	runtimeDir = dir
}

func withResolvectl(t *testing.T, path string) {
	t.Helper()
	orig := resolvectlOverride
	t.Cleanup(func() { resolvectlOverride = orig })
	resolvectlOverride = path
}

func TestDetect_NoRuntimeDir(t *testing.T) {
	withRuntimeDir(t, false)
	if detect() {
		t.Fatal("detect() = true with no runtime dir")
	}
}

func TestDetect_WithRuntimeDir(t *testing.T) {
	withRuntimeDir(t, true)
	if !detect() {
		t.Fatal("detect() = false with runtime dir present")
	}
}

// TestConfigure_HappyPath asserts both resolvectl invocations
// happen, in the right order, with the right args.
func TestConfigure_HappyPath(t *testing.T) {
	withRuntimeDir(t, true)
	bin := fakeResolvectl(t, 0, "")
	withResolvectl(t, bin)

	c := New()
	err := c.Configure(Config{
		Device:       "tun0",
		DNSAddress:   "fd00::1",
		SearchDomain: "hoop",
	})
	if err != nil {
		t.Fatalf("Configure: %v", err)
	}
	calls := readCalls(t, bin)
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d: %v", len(calls), calls)
	}
	want := []string{
		"dns tun0 fd00::1",
		"domain tun0 ~hoop",
	}
	for i, w := range want {
		if calls[i] != w {
			t.Errorf("call %d = %q, want %q", i, calls[i], w)
		}
	}
}

// TestConfigure_NoResolved exercises the "host doesn't run
// systemd-resolved" path. The fake resolvectl never gets called
// because detect() returns false.
func TestConfigure_NoResolved(t *testing.T) {
	withRuntimeDir(t, false)
	bin := fakeResolvectl(t, 0, "")
	withResolvectl(t, bin)

	c := New()
	err := c.Configure(Config{Device: "tun0", DNSAddress: "::1", SearchDomain: "hoop"})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
	if calls := readCalls(t, bin); len(calls) != 0 {
		t.Errorf("resolvectl was called %d time(s); should not have been: %v", len(calls), calls)
	}
}

// TestConfigure_DNSFailureSkipsDomain asserts that a failing
// `resolvectl dns` doesn't cascade into `resolvectl domain`.
// Important because the second call would silently succeed and
// leave a stale routing-domain entry pointing at a server that
// resolved doesn't know about.
func TestConfigure_DNSFailureSkipsDomain(t *testing.T) {
	withRuntimeDir(t, true)
	bin := fakeResolvectl(t, 1, "Failed to talk to system bus")
	withResolvectl(t, bin)

	c := New()
	err := c.Configure(Config{Device: "tun0", DNSAddress: "::1", SearchDomain: "hoop"})
	if err == nil {
		t.Fatal("expected error from failing resolvectl, got nil")
	}
	if !strings.Contains(err.Error(), "Failed to talk to system bus") {
		t.Errorf("error should include resolvectl output, got: %v", err)
	}
	calls := readCalls(t, bin)
	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 call (dns; domain should be skipped), got %d: %v", len(calls), calls)
	}
}

// TestConfigure_ValidationRejectsEmpty ensures we don't shell out
// at all when given obviously-broken inputs. Each empty field is
// a programming error in the caller and should not silently
// produce a useless `resolvectl dns  ::1` invocation.
func TestConfigure_ValidationRejectsEmpty(t *testing.T) {
	withRuntimeDir(t, true)
	bin := fakeResolvectl(t, 0, "")
	withResolvectl(t, bin)

	c := New()
	cases := []Config{
		{Device: "", DNSAddress: "::1", SearchDomain: "hoop"},
		{Device: "tun0", DNSAddress: "", SearchDomain: "hoop"},
		{Device: "tun0", DNSAddress: "::1", SearchDomain: ""},
	}
	for i, cfg := range cases {
		err := c.Configure(cfg)
		if err == nil {
			t.Errorf("case %d: expected error for invalid config %+v", i, cfg)
		}
	}
	if calls := readCalls(t, bin); len(calls) != 0 {
		t.Errorf("validation should short-circuit before resolvectl runs; got %d calls", len(calls))
	}
}

// TestUnconfigure_HappyPath verifies the revert call shape.
func TestUnconfigure_HappyPath(t *testing.T) {
	withRuntimeDir(t, true)
	bin := fakeResolvectl(t, 0, "")
	withResolvectl(t, bin)

	c := New()
	if err := c.Unconfigure("tun0"); err != nil {
		t.Fatalf("Unconfigure: %v", err)
	}
	calls := readCalls(t, bin)
	if len(calls) != 1 || calls[0] != "revert tun0" {
		t.Errorf("unexpected calls: %v", calls)
	}
}

// TestUnconfigure_DeadLinkIsNotError exercises the
// teardown-after-netstack-shutdown case. resolvectl prints "Link
// X does not exist" and exits non-zero; we treat that as success.
func TestUnconfigure_DeadLinkIsNotError(t *testing.T) {
	withRuntimeDir(t, true)
	bin := fakeResolvectl(t, 1, "Failed to revert link tun0: Link tun0 does not exist")
	withResolvectl(t, bin)

	c := New()
	if err := c.Unconfigure("tun0"); err != nil {
		t.Errorf("expected nil for 'Link does not exist', got: %v", err)
	}
}

// TestUnconfigure_NoResolvedIsNoOp covers running Unconfigure on a
// host that doesn't have systemd-resolved at all (could happen if
// the daemon was started on a resolved host then migrated to one
// without). Should silently succeed.
func TestUnconfigure_NoResolvedIsNoOp(t *testing.T) {
	withRuntimeDir(t, false)
	bin := fakeResolvectl(t, 0, "")
	withResolvectl(t, bin)

	c := New()
	if err := c.Unconfigure("tun0"); err != nil {
		t.Errorf("Unconfigure should be a no-op when resolved is absent, got: %v", err)
	}
	if calls := readCalls(t, bin); len(calls) != 0 {
		t.Errorf("resolvectl should not have been called: %v", calls)
	}
}

// --- helpers ---

func escapeShell(s string) string {
	// We never produce shell metachars in our test outputs; simple
	// double-quote is enough.
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func itoa(n int) string {
	switch {
	case n == 0:
		return "0"
	case n > 0:
		return string(rune('0'+n))
	default:
		return "-" + itoa(-n)
	}
}
