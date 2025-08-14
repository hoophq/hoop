package cmd

import (
	"os"

	"github.com/hoophq/hoop/agent"
	"github.com/hoophq/hoop/common/log"
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

		agent.Run()
	},
}

func init() {
	startAgentCmd.Flags().StringVar(&outputFormat, "format", os.Getenv("LOG_ENCODING"),
		"Output format: auto, human, verbose, console, json")

	startCmd.AddCommand(startAgentCmd)
	rootCmd.AddCommand(startCmd)
}
