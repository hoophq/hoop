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
	_ "github.com/hoophq/hoop/hoopinspect/analyzer/anthropic"
	_ "github.com/hoophq/hoop/hoopinspect/analyzer/openai"
	_ "github.com/hoophq/hoop/hoopinspect/analyzer/vertex"
	configyaml "github.com/hoophq/hoop/hoopinspect/config/yaml"
	"github.com/hoophq/hoop/hoopinspect/pii/alcatraz"
	"github.com/hoophq/hoop/hoopinspect/sidecar"
	"github.com/spf13/cobra"
)

// deprecatedSidecarAlias is the pre-rename name of this command. Cobra routes
// it to the same command, and the RunE prints a notice pointing at the new one.
const deprecatedSidecarAlias = "inspect"

var (
	sidecarConfigFlag   string
	sidecarValidateFlag bool
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

This command was named "inspect". That name still works as a deprecated
alias.`,
	Example: `  hoop start sidecar --config /etc/hoop-inspect/config.yaml
  hoop start sidecar --config config.yaml --validate`,
	// A bad config is not a usage error, and dumping the flag list under one
	// buries the message that says which field is wrong.
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		warnDeprecatedSidecarAlias(os.Stderr, cmd.CalledAs())

		if sidecarConfigFlag == "" {
			// The one genuine usage error here, so let cobra show the flags.
			cmd.SilenceUsage = false
			return fmt.Errorf("--config is required (or set HOOP_SIDECAR_CONFIG)")
		}

		cfg, det, err := sidecar.Setup(sidecarConfigFlag, configyaml.Load, buildSidecarPlugin)
		if err != nil {
			return err
		}

		if sidecarValidateFlag {
			lanes, err := sidecar.Validate(cfg, det)
			if err != nil {
				return err
			}
			fmt.Println("config OK:", len(lanes), "listener(s)")
			for _, ln := range lanes {
				fmt.Printf("  %-16s %-9s %s\n", ln.Name, ln.Protocol, ln.Summary())
			}
			return nil
		}

		// Run blocks until SIGINT or SIGTERM and installs its own handler.
		return sidecar.Run(cfg, det)
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
// A nil alcatraz.Plugin converts to a nil sidecar.Plugin, so an absent section
// stays nil rather than becoming a non-nil interface holding a nil pointer,
// which the sidecar would call through.
func buildSidecarPlugin(raw json.RawMessage) (sidecar.Plugin, error) {
	return alcatraz.PluginFromConfig(raw)
}

func init() {
	// The sidecar reports this at /stats and in its startup log, so an
	// operator reading either sees the hoop version that produced the binary
	// rather than the library's "dev" default.
	sidecar.Version = version.Get().Version

	startSidecarCmd.Flags().StringVar(&sidecarConfigFlag, "config", sidecarConfigFromEnv(),
		"Path to the inspection config file (YAML or JSON)")
	startSidecarCmd.Flags().BoolVar(&sidecarValidateFlag, "validate", false,
		"Validate the config, report what each listener resolved to, and exit")

	startCmd.AddCommand(startSidecarCmd)
}
