package cmd

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/daemon"
)

func icaConfig(names ...string) *daemon.Config {
	cfg := &daemon.Config{}
	for _, n := range names {
		cfg.Listeners = append(cfg.Listeners, daemon.ListenerConfig{Name: n, Protocol: "ica", Listen: ":2598", Upstream: "10.0.0.1:2598"})
	}
	return cfg
}

func noPath(string) (string, error) { return "", errors.New("not on PATH") }
func nothing(string) bool           { return false }
func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// A config without an ica lane never hands over, whatever is installed.
func TestICAHandoffIgnoresConfigsWithoutAnICALane(t *testing.T) {
	cfg := &daemon.Config{Listeners: []daemon.ListenerConfig{{Name: "db", Protocol: "postgres", Listen: ":1", Upstream: "h:1"}}}
	path, lanes, err := icaHandoff(cfg, envOf(map[string]string{hoopInspectEnv: "/x/hoop-inspect"}), noPath, func(string) bool { return true })
	if err != nil || path != "" || lanes != nil {
		t.Fatalf("got %q %v %v, want no handoff", path, lanes, err)
	}
}

// HOOP_INSPECT outranks PATH, PATH outranks the fixed locations, and the
// fixed locations are tried in order. The lane names come back so the
// notice can say which lane caused the handoff.
func TestICAHandoffLookupOrder(t *testing.T) {
	cfg := icaConfig("vdi", "vdi-2")
	cases := []struct {
		name   string
		env    map[string]string
		onPath string
		exists []string
		want   string
	}{
		{"env wins", map[string]string{hoopInspectEnv: "/opt/hi"}, "/usr/bin/hoop-inspect", []string{"/opt/hi", "/usr/local/bin/hoop-inspect"}, "/opt/hi"},
		{"PATH next", nil, "/usr/bin/hoop-inspect", []string{"/usr/local/bin/hoop-inspect"}, "/usr/bin/hoop-inspect"},
		{"installer path", nil, "", []string{"/usr/local/bin/hoop-inspect", "/app/hoop-inspect"}, "/usr/local/bin/hoop-inspect"},
		{"image path", nil, "", []string{"/app/hoop-inspect"}, "/app/hoop-inspect"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookPath := noPath
			if tc.onPath != "" {
				lookPath = func(string) (string, error) { return tc.onPath, nil }
			}
			exists := func(p string) bool { return slices.Contains(tc.exists, p) }
			path, lanes, err := icaHandoff(cfg, envOf(tc.env), lookPath, exists)
			if err != nil {
				t.Fatal(err)
			}
			if path != tc.want {
				t.Errorf("path = %q, want %q", path, tc.want)
			}
			if !slices.Equal(lanes, []string{"vdi", "vdi-2"}) {
				t.Errorf("lanes = %v", lanes)
			}
		})
	}
}

// Nothing installed: the error names the bundle and the override, since
// that is the whole fix. A HOOP_INSPECT that points nowhere is its own
// error rather than a silent fall-through to PATH.
func TestICAHandoffErrors(t *testing.T) {
	cfg := icaConfig("vdi")
	_, _, err := icaHandoff(cfg, envOf(nil), noPath, nothing)
	if err == nil || !strings.Contains(err.Error(), "hoop-ica-assets_") || !strings.Contains(err.Error(), hoopInspectEnv) {
		t.Fatalf("err = %v, want one naming the bundle and %s", err, hoopInspectEnv)
	}
	_, _, err = icaHandoff(cfg, envOf(map[string]string{hoopInspectEnv: "/nope"}), func(string) (string, error) { return "/usr/bin/hoop-inspect", nil }, nothing)
	if err == nil || !strings.Contains(err.Error(), "/nope") {
		t.Fatalf("err = %v, want the bad %s path", err, hoopInspectEnv)
	}
}

// Each flag crosses over iff set; -config always, as argv[1..2], since
// hoop-inspect has no environment default for it.
func TestICAHandoffArgs(t *testing.T) {
	got := icaHandoffArgs("/etc/hoop-inspect/config.yaml", "", "", false, false)
	if !slices.Equal(got, []string{"hoop-inspect", "-config", "/etc/hoop-inspect/config.yaml"}) {
		t.Errorf("bare: %v", got)
	}
	got = icaHandoffArgs("c.yaml", "/l.json", "hsc_x", true, true)
	want := []string{"hoop-inspect", "-config", "c.yaml", "-license", "/l.json", "-token", "hsc_x", "-validate", "-strict"}
	if !slices.Equal(got, want) {
		t.Errorf("all: %v, want %v", got, want)
	}
}
