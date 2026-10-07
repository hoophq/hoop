package cmd

import (
	"os"
	"path/filepath"

	"github.com/hoophq/hoop/agent"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/spf13/cobra"
)

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
	Use:          "gateway",
	Short:        "Runs the gateway component",
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		gateway.Run()
	},
}

var startControlPlaneCmd = &cobra.Command{
	Use:   "control-plane",
	Short: "Runs the gateway as the control plane",
	Long: `Runs the same gateway as "hoop start gateway". The control plane image
mounts no session volume, so PLUGIN_AUDIT_PATH defaults to a temporary
directory when it is not set.`,
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		// PLUGIN_AUDIT_PATH is consumed at package init time, so the resolved
		// variable is adjusted directly when the env was not provided.
		if os.Getenv("PLUGIN_AUDIT_PATH") == "" {
			auditPath := filepath.Join(os.TempDir(), "hoop_sessions")
			if err := os.MkdirAll(auditPath, 0o700); err != nil {
				log.Fatalf("failed creating the session storage directory %v: %v", auditPath, err)
			}
			plugintypes.AuditPath = auditPath
		}
		gateway.Run()
	},
}

func init() {
	startAgentCmd.Flags().StringVar(&outputFormat, "format", os.Getenv("LOG_ENCODING"),
		"Output format: auto, human, verbose, console, json")

	startCmd.AddCommand(startAgentCmd)
	startCmd.AddCommand(startGatewayCmd)
	startCmd.AddCommand(startControlPlaneCmd)
	rootCmd.AddCommand(startCmd)
}
