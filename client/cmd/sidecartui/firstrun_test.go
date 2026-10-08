package sidecartui

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// withProbes points Detect at ports this test controls, so a database
// running on the developer's machine cannot change the answer.
func withProbes(t *testing.T, p []struct {
	protocol string
	port     int
}) {
	t.Helper()
	orig := probes
	probes = p
	t.Cleanup(func() { probes = orig })
}

func fakeEnv(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func TestDetectPrefersDatabaseURLOverAnOpenPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	withProbes(t, []struct {
		protocol string
		port     int
	}{{"mysql", port}})

	in := Detect(context.Background(), fakeEnv(map[string]string{
		"DATABASE_URL":      "postgres://app:secret@db.internal/app",
		"ANTHROPIC_API_KEY": "sk-test",
	}))
	if in.Primary.Protocol != "postgres" || in.Primary.Addr != "db.internal:5432" || in.Primary.Source != "DATABASE_URL" {
		t.Errorf("primary = %+v, want postgres db.internal:5432 from DATABASE_URL", in.Primary)
	}
	if len(in.Others) != 1 || in.Others[0].Protocol != "mysql" {
		t.Errorf("others = %+v, want the open mysql probe", in.Others)
	}
	if in.AnalyzerProvider != "anthropic" {
		t.Errorf("analyzer provider = %q, want anthropic", in.AnalyzerProvider)
	}
}

func TestDetectFallsBackToALabelledDefault(t *testing.T) {
	withProbes(t, nil)
	in := Detect(context.Background(), fakeEnv(nil))
	if in.Primary.Protocol != "postgres" || in.Primary.Addr != "127.0.0.1:5432" {
		t.Errorf("primary = %+v, want the postgres default", in.Primary)
	}
	if !strings.HasPrefix(in.Primary.Source, "default") {
		t.Errorf("source = %q, want it to say it is a default", in.Primary.Source)
	}
	if in.AnalyzerProvider != "" {
		t.Errorf("analyzer provider = %q with no credential set", in.AnalyzerProvider)
	}
}

// The person may have edited the file a first w wrote. A second w, or a
// file with the same name from elsewhere, must never be replaced.
func TestWriteStarterNeverOverwrites(t *testing.T) {
	withProbes(t, nil)
	dir := t.TempDir()
	path := filepath.Join(dir, "hoop-sidecar.yaml")
	if err := os.WriteFile(path, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := WriteStarter(context.Background(), dir, fakeEnv(nil), nil)
	if !errors.Is(res.Err, errStarterExists) {
		t.Fatalf("err = %v, want errStarterExists", res.Err)
	}
	if b, _ := os.ReadFile(path); string(b) != "mine\n" {
		t.Errorf("the existing file was changed to %q", b)
	}
}

func TestWriteStarterValidatesWhatItWrote(t *testing.T) {
	withProbes(t, nil)
	dir := t.TempDir()
	var validated string
	res := WriteStarter(context.Background(), dir, fakeEnv(nil), func(p string) (string, error) {
		validated = p
		return "ok", nil
	})
	if res.Err != nil || res.ValidateErr != nil {
		t.Fatalf("WriteStarter: %v / %v", res.Err, res.ValidateErr)
	}
	if res.Path != validated || res.Path != filepath.Join(dir, "hoop-sidecar.yaml") {
		t.Errorf("path %q, validated %q", res.Path, validated)
	}
}

func TestFirstRunModelCountsVisits(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	m := newFirstRunModel("v1", func() time.Time { return now }, func() error { return nil }, nil)
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	tm, _ = tm.Update(frReadyMsg{url: "http://127.0.0.1:15321"})
	if out := ansi.Strip(tm.(firstRunModel).render()); !strings.Contains(out, "waiting for your browser") {
		t.Errorf("before a visit the screen should wait for the browser:\n%s", out)
	}
	tm, _ = tm.Update(frVisitMsg(now))
	tm, _ = tm.Update(frVisitMsg(now))
	out := ansi.Strip(tm.(firstRunModel).render())
	if !strings.Contains(out, "✓ opened") || !strings.Contains(out, "2 visits") {
		t.Errorf("after two visits the screen should say so:\n%s", out)
	}
	if !strings.Contains(out, "http://127.0.0.1:15321") {
		t.Errorf("the URL is not on screen:\n%s", out)
	}
}

func TestFirstRunModelShowsAWriteFailureLoudly(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	m := newFirstRunModel("v1", func() time.Time { return now }, func() error { return nil }, nil)
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	tm, _ = tm.Update(frWrittenMsg(StarterResult{Err: errors.New("hoop-sidecar.yaml already exists")}))
	out := ansi.Strip(tm.(firstRunModel).render())
	if !strings.Contains(out, "✕ hoop-sidecar.yaml already exists") {
		t.Errorf("a failed write must be on screen:\n%s", out)
	}
}

// Every size draws without panicking and never wider than the terminal.
func TestFirstRunModelRendersAtAnySize(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	for _, sz := range [][2]int{{20, 5}, {60, 14}, {72, 24}, {80, 30}, {200, 60}} {
		m := newFirstRunModel("v1", func() time.Time { return now }, func() error { return nil }, nil)
		var tm tea.Model = m
		tm, _ = tm.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		tm, _ = tm.Update(frReadyMsg{url: "http://127.0.0.1:15321"})
		tm, _ = tm.Update(frVisitMsg(now.Add(-300 * time.Millisecond)))
		out := tm.(firstRunModel).render()
		lines := strings.Split(out, "\n")
		if len(lines) > sz[1] {
			t.Errorf("%dx%d: %d lines", sz[0], sz[1], len(lines))
		}
		for _, l := range lines {
			if w := ansi.StringWidth(l); w > sz[0] {
				t.Errorf("%dx%d: a line is %d wide", sz[0], sz[1], w)
				break
			}
		}
	}
}
