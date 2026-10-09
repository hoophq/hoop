package cmd

import (
	"bytes"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoophq/hoop/client/cmd/sidecardemo"
	configyaml "github.com/hoophq/hoop/sidecar/config/yaml"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/spf13/cobra"
)

func writeDemoConfig(t *testing.T, demoAddr string) string {
	t.Helper()
	cfg := &daemon.Config{Listeners: []daemon.ListenerConfig{{
		Name: "demo", Protocol: "http", Listen: "127.0.0.1:0", Upstream: demoAddr,
	}}}
	b, err := configyaml.Render(cfg, configyaml.RenderOptions{
		Extensions: []configyaml.Extension{{Key: configyaml.DemoAPIKey, Value: demoAddr}},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), configyaml.StarterFile)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A demo config must bring its API up on every boot, not only the one the
// setup screen started, or `hoop start sidecar --config` on it later
// fronts nothing.
func TestStartSidecarDemoServesTheAPIAConfigNames(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	path := writeDemoConfig(t, addr)
	cfg, err := configyaml.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := startSidecarDemo(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer d.stop()
	ports, notes := d.ports, d.notes
	if ports == nil || ports.API != addr || ports.Listen != cfg.Listeners[0].Listen {
		t.Errorf("ports = %+v, want the API %s and the listener in front of it", ports, addr)
	}
	if len(notes) == 0 || !strings.Contains(notes[0], addr) {
		t.Errorf("notes = %v, want them to name %s", notes, addr)
	}
	resp, err := http.Get("http://" + addr + "/users")
	if err != nil {
		t.Fatalf("the demo API is not up: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestStartSidecarDemoRefusesANonLoopbackAddress(t *testing.T) {
	if _, err := startSidecarDemo(writeDemoConfig(t, "0.0.0.0:18081"), &daemon.Config{}); err == nil {
		t.Fatal("the demo API was allowed to listen beyond loopback")
	}
}

// localhost is bound as 127.0.0.1, never as whatever the resolver maps the
// name to: the demo API serves this machine only.
func TestLoopbackBind(t *testing.T) {
	for in, want := range map[string]string{
		"localhost:18081": "127.0.0.1:18081", "127.0.0.1:18081": "127.0.0.1:18081", "[::1]:18081": "[::1]:18081",
	} {
		if got, err := loopbackBind(in); err != nil || got != want {
			t.Errorf("loopbackBind(%s) = %s, %v; want %s", in, got, err, want)
		}
	}
	for _, in := range []string{"0.0.0.0:18081", "10.0.0.5:18081", "example.com:18081", "nope"} {
		if _, err := loopbackBind(in); err == nil {
			t.Errorf("loopbackBind(%s) accepted a non-loopback address", in)
		}
	}
}

// With no listener in front of the demo API, Try it stays off: its
// requests would reach a default port and another service, or nothing.
func TestStartSidecarDemoWithoutAListenerForItHasNoTour(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	d, err := startSidecarDemo(writeDemoConfig(t, addr), &daemon.Config{Listeners: []daemon.ListenerConfig{
		{Name: "other", Protocol: "http", Listen: "127.0.0.1:0", Upstream: "127.0.0.1:1"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.stop()
	if d.ports != nil {
		t.Errorf("tour ports = %+v with no listener forwarding to %s", d.ports, addr)
	}
	if len(d.notes) != 1 || !strings.Contains(d.notes[0], "no listener forwards to it") {
		t.Errorf("notes = %v, want them to say what is missing", d.notes)
	}
}

// A demo API that stops serving is reported, not dropped.
func TestADemoAPIFailureIsReported(t *testing.T) {
	d := &sidecarDemo{stop: func() {}}
	errc := make(chan error, 1)
	errc <- errors.New("accept: too many open files")
	d.watch(errc)
	if err := d.err(); err == nil || !strings.Contains(err.Error(), "too many open files") {
		t.Errorf("err = %v", err)
	}
}

func TestStartSidecarDemoIgnoresAConfigWithoutTheKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte("listeners: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := startSidecarDemo(path, &daemon.Config{})
	if err != nil || d.notes != nil || d.ports != nil {
		t.Fatalf("notes %v, err %v; want nothing started", d.notes, err)
	}
	d.stop()
}

// The Connect page's check is the boot's own handshake: an unreachable
// plane fails it, and the URL it set for the call does not outlive it.
func TestCheckControlPlaneReportsAnUnreachablePlane(t *testing.T) {
	t.Setenv(daemon.ControlPlaneURLEnv, "")
	t.Setenv("HOOP_LICENSE", "")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing answers here now
	if _, err := checkControlPlane("http://"+addr, "hsc_test"); err == nil {
		t.Fatal("a plane nobody answers for passed the check")
	}
	if v := os.Getenv(daemon.ControlPlaneURLEnv); v != "" {
		t.Errorf("%s = %q after the check, want it restored", daemon.ControlPlaneURLEnv, v)
	}
}

// The setup screen's validator is the boot's own path: Setup, then
// Validate. It must accept the demo config the screen writes.
func TestValidateSidecarConfigAcceptsTheDemo(t *testing.T) {
	t.Setenv(daemon.ControlPlaneURLEnv, "")
	t.Setenv("HOOP_LICENSE", "")
	summary, err := validateSidecarConfig(writeDemoConfig(t, sidecardemo.Addr))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(summary, "1 listener") {
		t.Errorf("summary = %q", summary)
	}
}

func TestStartSidecarCommandNames(t *testing.T) {
	for _, name := range []string{"sidecar", "inspect"} {
		t.Run(name, func(t *testing.T) {
			got, _, err := startCmd.Find([]string{name})
			if err != nil {
				t.Fatalf("resolving %q: %v", name, err)
			}
			if got != startSidecarCmd {
				t.Fatalf("%q resolved to %q, want the sidecar command", name, got.Name())
			}
		})
	}
}

func TestWarnDeprecatedSidecarAlias(t *testing.T) {
	for _, tt := range []struct {
		msg      string
		calledAs string
		wantWarn bool
	}{
		{msg: "the old name warns", calledAs: "inspect", wantWarn: true},
		{msg: "the new name is silent", calledAs: "sidecar"},
		{msg: "an empty invocation name is silent", calledAs: ""},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			var buf bytes.Buffer
			warnDeprecatedSidecarAlias(&buf, tt.calledAs)

			got := buf.String()
			if !tt.wantWarn {
				if got != "" {
					t.Fatalf("want no output, got %q", got)
				}
				return
			}
			if !strings.Contains(got, "hoop start sidecar") {
				t.Fatalf("warning does not name the new command: %q", got)
			}
			if !strings.Contains(got, "deprecated") {
				t.Fatalf("warning does not say deprecated: %q", got)
			}
		})
	}
}

func TestSidecarConfigFromEnv(t *testing.T) {
	for _, tt := range []struct {
		msg     string
		sidecar string
		inspect string
		want    string
	}{
		{msg: "the current name wins", sidecar: "/new.yaml", inspect: "/old.yaml", want: "/new.yaml"},
		{msg: "the pre-rename name still works", inspect: "/old.yaml", want: "/old.yaml"},
		{msg: "an empty current name falls back", sidecar: "", inspect: "/old.yaml", want: "/old.yaml"},
		{msg: "neither set resolves to empty"},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			t.Setenv("HOOP_SIDECAR_CONFIG", tt.sidecar)
			t.Setenv("HOOP_INSPECT_CONFIG", tt.inspect)

			if got := sidecarConfigFromEnv(); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSidecarBareInvocation(t *testing.T) {
	for _, tt := range []struct {
		msg  string
		args []string
		want bool
	}{
		{msg: "nothing at all is bare", want: true},
		{msg: "--validate keeps the usage error", args: []string{"--validate"}},
		{msg: "--strict keeps the usage error", args: []string{"--strict"}},
		{msg: "--license keeps the usage error", args: []string{"--license", "/lic.json"}},
		{msg: "--token keeps the usage error", args: []string{"--token", "hsc_x"}},
		{msg: "an explicit default value still keeps the usage error", args: []string{"--validate=false"}},
		{msg: "an explicit empty value still keeps the usage error", args: []string{"--license="}},
		{msg: "an inherited global flag keeps the usage error", args: []string{"--debug"}},
		{msg: "a positional argument keeps the usage error", args: []string{"extra"}},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			// Fresh commands per case: pflag's Changed sticks once set, so
			// reusing the package-level command would leak state across cases.
			root := &cobra.Command{Use: "hoop"}
			root.PersistentFlags().Bool("debug", false, "")
			cmd := &cobra.Command{Use: "sidecar"}
			cmd.Flags().Bool("validate", false, "")
			cmd.Flags().Bool("strict", false, "")
			cmd.Flags().String("license", "", "")
			cmd.Flags().String("token", "", "")
			root.AddCommand(cmd)
			if err := cmd.ParseFlags(tt.args); err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := sidecarBareInvocation(cmd, cmd.Flags().Args()); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
