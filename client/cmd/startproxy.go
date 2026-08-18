package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/hoophq/hoop/common/version"
	// Analyzer providers register themselves on import, matching the
	// standalone hoop-inspect binary. Linking all three keeps the
	// config-decides-everything rule: turning on Vertex must not require a
	// different binary.
	_ "github.com/hoophq/hoopinspect/analyzer/anthropic"
	_ "github.com/hoophq/hoopinspect/analyzer/openai"
	_ "github.com/hoophq/hoopinspect/analyzer/vertex"
	configyaml "github.com/hoophq/hoopinspect/config/yaml"
	"github.com/hoophq/hoopinspect/pii/alcatraz"
	"github.com/hoophq/hoopinspect/sidecar"
	"github.com/spf13/cobra"
)

var (
	proxyConfigFlag   string
	proxyValidateFlag bool
)

const proxyLongHelp = `Runs the hoop proxy: an inspecting relay that decodes the wire protocol
between a client and a database or API, evaluates each statement against
policy, records an audit trail, and masks sensitive values on the way back.

It routes nothing and terminates no DOWNSTREAM TLS. Run it behind something
that already owns the network path and identity, typically an Envoy sidecar
forwarding plaintext over loopback or a unix socket. The hop to the backend
can be TLS (upstream_tls): the relay originates it and still inspects, since
it is the client on that hop and decrypts what it reads.

Every capability is decided by the config file, so turning on PII detection
does not require a different binary. The file may be YAML or JSON; the
extension picks the parser.`

var startProxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Runs the inspecting proxy",
	Long:  proxyLongHelp,
	Example: `  hoop start proxy --config /etc/hoop-proxy/config.yaml
  hoop start proxy --config config.yaml --validate`,
	// A bad config is not a usage error, and dumping the flag list under one
	// buries the message that says which field is wrong.
	SilenceUsage: true,
	RunE:         runProxy,
}

// startInspectCmd is the former spelling, kept working.
//
// cobra's Deprecated field does two things that an Aliases entry does not: it
// prints the notice below before running, and it drops the command from help
// output. So an existing script keeps working and says so once per run, while
// nothing points a new reader at the old name.
//
// It shares RunE and the flag variables with the command above rather than
// delegating, because a second code path is a second thing to keep correct —
// and the failure mode would be the deprecated form quietly behaving
// differently from the one it tells you to switch to.
var startInspectCmd = &cobra.Command{
	Use:          "inspect",
	Short:        "DEPRECATED: use \"hoop start proxy\"",
	Long:         "DEPRECATED: renamed to \"hoop start proxy\". This spelling still works.\n\n" + proxyLongHelp,
	Deprecated:   "use \"hoop start proxy\" instead.",
	SilenceUsage: true,
	RunE:         runProxy,
}

func runProxy(cmd *cobra.Command, args []string) error {
	if proxyConfigFlag == "" {
		// The one genuine usage error here, so let cobra show the flags.
		cmd.SilenceUsage = false
		return fmt.Errorf("--config is required (or set HOOP_PROXY_CONFIG)")
	}

	cfg, det, err := sidecar.Setup(proxyConfigFlag, configyaml.Load, buildProxyPlugin)
	if err != nil {
		return err
	}

	if proxyValidateFlag {
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
}

// buildProxyPlugin constructs the PII detector from the config's "pii"
// section.
//
// A nil alcatraz.Plugin converts to a nil sidecar.Plugin, so an absent section
// stays nil rather than becoming a non-nil interface holding a nil pointer,
// which the sidecar would call through.
func buildProxyPlugin(raw json.RawMessage) (sidecar.Plugin, error) {
	return alcatraz.PluginFromConfig(raw)
}

// proxyConfigEnv resolves the config path from the environment.
//
// HOOP_PROXY_CONFIG is the name; HOOP_INSPECT_CONFIG still works so a
// deployment that set it keeps running, and the new name wins when both are
// present rather than the resolution depending on which was exported last.
func proxyConfigEnv() string {
	if v := os.Getenv("HOOP_PROXY_CONFIG"); v != "" {
		return v
	}
	return os.Getenv("HOOP_INSPECT_CONFIG")
}

func init() {
	// The sidecar reports this at /stats and in its startup log, so an
	// operator reading either sees the hoop version that produced the binary
	// rather than the library's "dev" default.
	sidecar.Version = version.Get().Version

	// Both commands bind the SAME variables, so whichever one runs sees the
	// flags it was given. Cobra parses flags per invoked command, and only one
	// of these is ever invoked.
	for _, c := range []*cobra.Command{startProxyCmd, startInspectCmd} {
		c.Flags().StringVar(&proxyConfigFlag, "config", proxyConfigEnv(),
			"Path to the proxy config file (YAML or JSON). Defaults to $HOOP_PROXY_CONFIG")
		c.Flags().BoolVar(&proxyValidateFlag, "validate", false,
			"Validate the config, report what each listener resolved to, and exit")
		startCmd.AddCommand(c)
	}
}
