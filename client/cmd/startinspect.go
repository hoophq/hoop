package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/hoophq/hoop/common/version"
	configyaml "github.com/hoophq/hoopinspect/config/yaml"
	"github.com/hoophq/hoopinspect/pii/alcatraz"
	"github.com/hoophq/hoopinspect/sidecar"
	"github.com/spf13/cobra"
)

var (
	inspectConfigFlag   string
	inspectValidateFlag bool
)

var startInspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Runs the inspection sidecar",
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
extension picks the parser.`,
	Example: `  hoop start inspect --config /etc/hoop-inspect/config.yaml
  hoop start inspect --config config.yaml --validate`,
	// A bad config is not a usage error, and dumping the flag list under one
	// buries the message that says which field is wrong.
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if inspectConfigFlag == "" {
			// The one genuine usage error here, so let cobra show the flags.
			cmd.SilenceUsage = false
			return fmt.Errorf("--config is required (or set HOOP_INSPECT_CONFIG)")
		}

		cfg, det, err := sidecar.Setup(inspectConfigFlag, configyaml.Load, buildInspectPlugin)
		if err != nil {
			return err
		}

		if inspectValidateFlag {
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

// buildInspectPlugin constructs the PII detector from the config's "pii"
// section.
//
// A nil alcatraz.Plugin converts to a nil sidecar.Plugin, so an absent section
// stays nil rather than becoming a non-nil interface holding a nil pointer,
// which the sidecar would call through.
func buildInspectPlugin(raw json.RawMessage) (sidecar.Plugin, error) {
	return alcatraz.PluginFromConfig(raw)
}

func init() {
	// The sidecar reports this at /stats and in its startup log, so an
	// operator reading either sees the hoop version that produced the binary
	// rather than the library's "dev" default.
	sidecar.Version = version.Get().Version

	startInspectCmd.Flags().StringVar(&inspectConfigFlag, "config", os.Getenv("HOOP_INSPECT_CONFIG"),
		"Path to the inspection config file (YAML or JSON)")
	startInspectCmd.Flags().BoolVar(&inspectValidateFlag, "validate", false,
		"Validate the config, report what each listener resolved to, and exit")

	startCmd.AddCommand(startInspectCmd)
}
