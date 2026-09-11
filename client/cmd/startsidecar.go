package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/hoophq/hoop/client/cmd/styles"
	"github.com/hoophq/hoop/common/version"
	// Analyzer providers register themselves on import, matching the
	// standalone hoop-inspect binary. Linking all three keeps the
	// config-decides-everything rule: turning on Vertex must not require a
	// different binary.
	_ "github.com/hoophq/hoop/sidecar/analyzer/anthropic"
	_ "github.com/hoophq/hoop/sidecar/analyzer/openai"
	_ "github.com/hoophq/hoop/sidecar/analyzer/vertex"
	configyaml "github.com/hoophq/hoop/sidecar/config/yaml"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/pii/alcatraz"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// deprecatedSidecarAlias is the pre-rename name of this command. Cobra routes
// it to the same command, and the RunE prints a notice pointing at the new one.
const deprecatedSidecarAlias = "inspect"

var (
	sidecarConfigFlag     string
	sidecarLicenseFlag    string
	sidecarTokenFlag      string
	sidecarValidateFlag   bool
	sidecarStrictFlag     bool
	sidecarMigrateFlag    bool
	sidecarMigrateOutFlag string
)

var startSidecarCmd = &cobra.Command{
	Use:     "sidecar",
	Aliases: []string{deprecatedSidecarAlias},
	Short:   "Runs the inspection sidecar",
	Long: `Runs the hoop-inspect relay: an inspecting proxy that decodes the wire
protocol between a client and a database or API, evaluates each statement
against policy, records an audit trail, and masks sensitive values on the way
back.

It routes nothing and terminates no DOWNSTREAM TLS. Run it behind something
that already owns the network path and identity, typically an Envoy sidecar
forwarding plaintext over loopback or a unix socket. The hop to the backend
can be TLS (upstream_tls): the relay originates it and still inspects, since
it is the client on that hop and decrypts what it reads.

Every capability is decided by the config file, so turning on PII detection
does not require a different binary. The file may be YAML or JSON; the
extension picks the parser.

Run with nothing at all — no config, no flags — and a built-in default
starts instead: one loopback URL that forwards to the getting-started
guide. It inspects no traffic; it exists so the first run after the
install works. Write a config to replace it.

This command was named "inspect". That name still works as a deprecated
alias.

The config schema changed in ADR-0011: "policy" split into "guardrails" and
"opa", and three fields were dropped. Both spellings load, and the old one
prints a warning naming its replacement. Use --strict to fail on one.

Without a license the process caps guardrail and data masking rules at one
each and says so at startup. A license lifts the caps for the features it
names. It may be a path or the document itself, and --license outranks
HOOP_LICENSE, which outranks the "license" key in the config file.

A sidecar may connect to a Control Plane instead of carrying its own
listeners: set HOOP_CONTROL_PLANE_URL or the "control_plane_url" config key
(the env var outranks the key), and pass the token from the sidecar's
registration with --token, which outranks HOOP_SIDECAR_TOKEN. The handshake
then supplies the whole running config and --config becomes optional. A
Control Plane holding no configuration is seeded with the config file's
document on that first handshake, so an existing sidecar connects by adding
the URL and passing the token, nothing else; once the plane holds a config
it owns it, and listeners still in the file are ignored with a warning. The
token is shown once when the sidecar is created; a lost one means
registering a new sidecar.`,
	Example: `  hoop start sidecar
  hoop start sidecar --config /etc/hoop-inspect/config.yaml
  hoop start sidecar --config config.yaml --license /etc/hoop-inspect/license.json
  hoop start sidecar --config config.yaml --validate
  hoop start sidecar --config config.yaml --validate --strict
  HOOP_CONTROL_PLANE_URL=https://cp.example.com hoop start sidecar --token hsc_...`,
	// A bad config is not a usage error, and dumping the flag list under one
	// buries the message that says which field is wrong.
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		warnDeprecatedSidecarAlias(os.Stderr, cmd.CalledAs())

		if sidecarConfigFlag == "" && os.Getenv(daemon.ControlPlaneURLEnv) == "" {
			if sidecarBareInvocation(cmd, args) {
				return daemon.FirstRun(os.Stdout, "hoop start sidecar --config config.yaml")
			}
			// The one genuine usage error here, so let cobra show the flags.
			cmd.SilenceUsage = false
			return fmt.Errorf("--config is required (or set HOOP_SIDECAR_CONFIG or %s)",
				daemon.ControlPlaneURLEnv)
		}
		if sidecarMigrateFlag {
			if sidecarConfigFlag == "" {
				cmd.SilenceUsage = false
				return fmt.Errorf("--migrate needs --config: it rewrites a file, " +
					"and a control-plane config has no file to rewrite")
			}
			cfg, err := configyaml.Load(sidecarConfigFlag)
			if err != nil {
				return err
			}
			out := os.Stdout
			if sidecarMigrateOutFlag != "" {
				f, err := os.Create(sidecarMigrateOutFlag)
				if err != nil {
					return err
				}
				defer f.Close()
				out = f
			}
			// The destination's extension picks the syntax; stdout
			// inherits the input's.
			target := sidecarMigrateOutFlag
			if target == "" {
				target = sidecarConfigFlag
			}
			return daemon.WriteMigrated(cfg, configyaml.IsYAML(target), out, os.Stderr)
		}

		cfg, det, err := daemon.SetupWith(sidecarConfigFlag, configyaml.Load, buildSidecarPlugin,
			daemon.WithLicense(sidecarLicenseFlag),
			daemon.WithControlPlaneToken(sidecarTokenFlag))
		if err != nil {
			return err
		}

		// Same channel as the rename notice above, and for the same reason:
		// --validate writes a parseable report to stdout.
		daemon.ReportDeprecations(os.Stderr, cfg.Deprecations)
		if sidecarStrictFlag && len(cfg.Deprecations) > 0 {
			return fmt.Errorf("%d deprecated config field(s) in use and --strict is set",
				len(cfg.Deprecations))
		}

		if sidecarValidateFlag {
			lanes, err := daemon.Validate(cfg, det)
			if err != nil {
				return err
			}
			return daemon.PrintLanes(os.Stdout, cfg.Licensing(), lanes)
		}

		// Run blocks until SIGINT or SIGTERM and installs its own handler.
		return daemon.Run(cfg, det)
	},
}

// warnDeprecatedSidecarAlias renders the rename notice to w when the command
// was reached through the old name. calledAs is the token the user typed, so
// the notice stays silent for the new name.
//
// It goes to stderr: --validate writes a report to stdout that an operator may
// parse, and a warning must not land in it.
func warnDeprecatedSidecarAlias(w io.Writer, calledAs string) {
	if calledAs != deprecatedSidecarAlias {
		return
	}
	msg := styles.ClientErrorSimple(fmt.Sprintf(
		"warn: \"hoop start %s\" is deprecated and aliases to \"hoop start sidecar\".\n"+
			"Use \"hoop start sidecar\"; the alias is removed in a future release.",
		deprecatedSidecarAlias))
	_, _ = fmt.Fprintf(w, "%s\n", msg)
}

// sidecarBareInvocation reports whether this invocation asked for nothing:
// no config anywhere, no control plane, no flag, no argument. That is the
// one case that runs the first-run default (daemon.FirstRun) — a loopback
// URL forwarding to the getting-started guide — instead of a usage error,
// so a user's first contact after the install is a working URL.
//
// The gate keys on what the user typed, not on resulting values: a flag
// set to its default (--validate=false, --license=) or an inherited global
// flag (--debug) still keeps the error, because typing anything without a
// config is a mistake to report, not a request for the demo.
//
// The caller has already established that sidecarConfigFlag (whose default
// comes from the environment) and the control plane env var are empty.
func sidecarBareInvocation(cmd *cobra.Command, args []string) bool {
	if len(args) > 0 {
		return false
	}
	changed := false
	seen := func(f *pflag.Flag) { changed = changed || f.Changed }
	cmd.Flags().VisitAll(seen)
	cmd.InheritedFlags().VisitAll(seen)
	return !changed
}

// sidecarConfigFromEnv reads the config path from the environment. It prefers
// the current name and falls back to the pre-rename one, so a deployment that
// still sets HOOP_INSPECT_CONFIG keeps working.
func sidecarConfigFromEnv() string {
	if v := os.Getenv("HOOP_SIDECAR_CONFIG"); v != "" {
		return v
	}
	return os.Getenv("HOOP_INSPECT_CONFIG")
}

// buildSidecarPlugin constructs the PII detector from the config's "pii"
// section.
//
// An absent section no longer disables detection: the plugin builds a
// detector over every entity type it knows, and the section narrows it. A nil
// Plugin means only that a build linked no detector, which this one does not.
//
// The conversion still matters. A nil alcatraz.Plugin converts to a nil
// daemon.Plugin rather than to a non-nil interface holding a nil pointer,
// which the sidecar would call through.
func buildSidecarPlugin(raw json.RawMessage) (daemon.Plugin, error) {
	return alcatraz.PluginFromConfig(raw)
}

func init() {
	// The sidecar reports this at /stats and in its startup log, so an
	// operator reading either sees the hoop version that produced the binary
	// rather than the library's "dev" default.
	daemon.Version = version.Get().Version
	// --migrate renders YAML through the same module that parses it; the
	// daemon package cannot import it, so the renderer is injected.
	daemon.YAMLFromJSON = configyaml.FromJSON

	startSidecarCmd.Flags().StringVar(&sidecarConfigFlag, "config", sidecarConfigFromEnv(),
		"Path to the inspection config file (YAML or JSON)")
	// No default from the environment, unlike --config. daemon.Setup reads
	// HOOP_LICENSE when this is empty, which holds the precedence in one
	// place and keeps a customer's license out of --help.
	startSidecarCmd.Flags().StringVar(&sidecarLicenseFlag, "license", "",
		"Path to the license file, or the license document itself. Overrides "+
			license.EnvVar+" and the config file's \"license\" key")
	// Same rule as --license: no default from the environment, so daemon
	// setup holds the precedence in one place and the token stays out of
	// --help output.
	startSidecarCmd.Flags().StringVar(&sidecarTokenFlag, "token", "",
		"The token identifying this sidecar to the control plane. Overrides "+
			daemon.SidecarTokenEnv)
	startSidecarCmd.Flags().BoolVar(&sidecarValidateFlag, "validate", false,
		"Validate the config, report what each listener resolved to, and exit")
	startSidecarCmd.Flags().BoolVar(&sidecarStrictFlag, "strict", false,
		"Fail when the config uses a deprecated field, so a pipeline can catch it "+
			"before the release that removes it")
	startSidecarCmd.Flags().BoolVar(&sidecarMigrateFlag, "migrate", false,
		"Rewrite the config onto the current schema, print it, and exit. Deprecated "+
			"fields are folded and ai_analysis rules become listener analyzer blocks "+
			"where the move is faithful")
	startSidecarCmd.Flags().StringVar(&sidecarMigrateOutFlag, "migrate-out", "",
		"File --migrate writes to instead of stdout; its extension picks the syntax, "+
			"defaulting to the input's")

	startCmd.AddCommand(startSidecarCmd)
}
