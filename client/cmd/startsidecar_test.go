package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configyaml "github.com/hoophq/hoop/sidecar/config/yaml"
	"github.com/spf13/cobra"
)

// The starter config is the free tier's front door: it must pass the full
// --validate path, caps included, with no license, for every protocol the
// first-run screen can infer and with each analyzer hint.
func TestStarterConfigPassesValidateOnTheFreeTier(t *testing.T) {
	addrs := map[string]string{
		"postgres": "127.0.0.1:5432", "mysql": "127.0.0.1:3306", "mssql": "127.0.0.1:1433",
		"oracle": "127.0.0.1:1521", "clickhouse": "127.0.0.1:8123", "mongodb": "127.0.0.1:27017",
		"http": "127.0.0.1:8080",
	}
	for p, addr := range addrs {
		for _, provider := range []string{"", "anthropic", "vertex"} {
			b, err := configyaml.Starter(configyaml.StarterInput{
				Primary:          configyaml.Upstream{Protocol: p, Addr: addr, Source: "test"},
				AnalyzerProvider: provider,
			})
			if err != nil {
				t.Fatalf("Starter(%s): %v", p, err)
			}
			path := filepath.Join(t.TempDir(), configyaml.StarterFile)
			if err := os.WriteFile(path, b, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := validateStarter(path); err != nil {
				t.Errorf("Starter(%s, %q) fails --validate: %v\n%s", p, provider, err, b)
			}
		}
	}
}

// The file promises the analyzer is one uncomment away. Hold it to that:
// store a key where the comment says, uncomment both blocks, and the
// result must pass --validate with the provider built.
func TestStarterAnalyzerIsOneUncommentAway(t *testing.T) {
	keyDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(keyDir, "anthropic.key"), []byte("sk-test"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := configyaml.Starter(configyaml.StarterInput{
		Primary:          configyaml.Upstream{Protocol: "postgres", Addr: "127.0.0.1:5432", Source: "test"},
		AnalyzerProvider: "anthropic",
		KeyDir:           keyDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	inLane, inTop := false, false
	for _, l := range strings.Split(string(b), "\n") {
		switch {
		case l == "    # analyzer:":
			inLane = true
		case l == "# analyzer:":
			inTop = true
		}
		switch {
		case inLane && strings.HasPrefix(l, "    # "):
			l = "    " + strings.TrimPrefix(l, "    # ")
			inLane = !strings.Contains(l, "low:")
		case inTop && strings.HasPrefix(l, "# "):
			l = strings.TrimPrefix(l, "# ")
		}
		out = append(out, l)
	}
	path := filepath.Join(t.TempDir(), configyaml.StarterFile)
	if err := os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := configyaml.Load(path)
	if err != nil {
		t.Fatalf("uncommented starter does not load: %v\n%s", err, strings.Join(out, "\n"))
	}
	if cfg.Analyzer == nil || cfg.Analyzer.Provider != "anthropic" || cfg.Listeners[0].Analyzer == nil {
		t.Fatalf("the uncommented analyzer did not reach the config:\n%s", strings.Join(out, "\n"))
	}
	if _, err := validateStarter(path); err != nil {
		t.Errorf("uncommented starter fails --validate: %v", err)
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
