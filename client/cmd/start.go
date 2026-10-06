package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/hoophq/hoop/agent"
	"github.com/hoophq/hoop/client/cmd/styles"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway"
	"github.com/spf13/cobra"
)

// deprecatedGatewayAlias is the subcommand that ran the HTTP API alone, to
// administer sidecars. One gateway serves agents and sidecars now; the alias
// keeps existing installs starting.
const deprecatedGatewayAlias = "control-plane"

var (
	outputFormat string
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start one of the Hoop component",
}

var startAgentCmd = &cobra.Command{
	Use:          "agent",
	Short:        "Runs the agent component",
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		// Use verbose format when global debug flag is enabled
		if debugFlag && outputFormat == "" {
			outputFormat = "verbose"
		}

		switch outputFormat {
		case "human":
			os.Setenv("LOG_ENCODING", "human")
		case "verbose":
			os.Setenv("LOG_ENCODING", "verbose")
		case "console":
			os.Setenv("LOG_ENCODING", "console")
		case "json":
			os.Setenv("LOG_ENCODING", "json")
		default:
			// Auto-detect format based on output destination
			if fileInfo, err := os.Stdout.Stat(); err == nil {
				if (fileInfo.Mode() & os.ModeCharDevice) != 0 {
					os.Setenv("LOG_ENCODING", "human")
				} else {
					os.Setenv("LOG_ENCODING", "json")
				}
			} else {
				// Fallback to JSON if we can't determine output type
				os.Setenv("LOG_ENCODING", "json")
			}
		}

		log.ReinitializeLogger()

		//TODO for now we will start the rust agent if hoop_rs binary is found
		// if something goes wrong we will fallback we will skill but alwys run the go agent
		// check if gateway env is set
		if os.Getenv("HOOP_GATEWAY_URL") != "" {
			RunAgentrs()
		} else {
			log.Info("HOOP_GATEWAY_URL not set, skipping hoop_rs agent startup")
		}
		agent.Run()
	},
}

var startGatewayCmd = &cobra.Command{
	Use:     "gateway",
	Aliases: []string{deprecatedGatewayAlias},
	Short:   "Runs the gateway component",
	Long: `Runs the gateway: the gRPC transport for agents and clients, the protocol
proxies, and the HTTP API and web app that also administer a fleet of
sidecars.

This command was also reachable as "control-plane". That name still works as
a deprecated alias and starts the same gateway.`,
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		warnDeprecatedGatewayAlias(os.Stderr, cmd.CalledAs())
		gateway.Run()
	},
}

// warnDeprecatedGatewayAlias renders the notice to w when the command was
// reached through the old name. calledAs is the token the user typed.
func warnDeprecatedGatewayAlias(w io.Writer, calledAs string) {
	if calledAs != deprecatedGatewayAlias {
		return
	}
	msg := styles.ClientErrorSimple(fmt.Sprintf(
		"warn: \"hoop start %s\" is deprecated and aliases to \"hoop start gateway\".\n"+
			"Use \"hoop start gateway\"; the alias is removed in a future release.",
		deprecatedGatewayAlias))
	_, _ = fmt.Fprintf(w, "%s\n", msg)
}

func init() {
	startAgentCmd.Flags().StringVar(&outputFormat, "format", os.Getenv("LOG_ENCODING"),
		"Output format: auto, human, verbose, console, json")

	startCmd.AddCommand(startAgentCmd)
	startCmd.AddCommand(startGatewayCmd)
	rootCmd.AddCommand(startCmd)
}
