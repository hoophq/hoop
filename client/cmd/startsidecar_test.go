package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

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
