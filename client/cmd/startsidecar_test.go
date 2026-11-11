package cmd

import (
	"bytes"
	"net"
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
		// Picking the output is not asking for a config: it is how to get
		// the first run without the screen.
		{msg: "--log-format text is the first run without the screen", args: []string{"--log-format", "text"}, want: true},
		{msg: "--log-format json is the first run without the screen", args: []string{"--log-format=json"}, want: true},
		{msg: "--log-format with another flag keeps the usage error", args: []string{"--log-format", "text", "--validate"}},
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
			cmd.Flags().String("log-format", "auto", "")
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
