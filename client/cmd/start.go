package cmd

import (
	"os"

	"github.com/hoophq/hoop/agent"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway"
	"github.com/spf13/cobra"
)

var (
	outputFormat string
	verboseMode  bool
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
		// Se verbose está ativo, força o formato verbose
		if verboseMode && outputFormat == "auto" {
			outputFormat = "verbose"
		}

		// Configura o formato de log baseado nas flags
		if outputFormat == "auto" || outputFormat == "" {
			// Auto-detecta baseado em TTY usando fileinfo
			if fileInfo, _ := os.Stdout.Stat(); (fileInfo.Mode() & os.ModeCharDevice) != 0 {
				os.Setenv("LOG_ENCODING", "human")
			} else {
				os.Setenv("LOG_ENCODING", "json")
			}
		} else {
			switch outputFormat {
			case "human":
				os.Setenv("LOG_ENCODING", "human")
			case "verbose":
				os.Setenv("LOG_ENCODING", "verbose")
			case "console":
				os.Setenv("LOG_ENCODING", "console")
			case "json":
				os.Setenv("LOG_ENCODING", "json")
			}
		}

		// Verbose mode = debug level
		if verboseMode || outputFormat == "verbose" {
			os.Setenv("LOG_LEVEL", "DEBUG")
		}

		// Reinicializa o logger com as novas configurações
		log.ReinitializeLogger()

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

func init() {
	// Adiciona flags para o comando agent
	startAgentCmd.Flags().StringVar(&outputFormat, "format", "auto",
		"Output format: auto, human, verbose, json (default \"auto\")")
	startAgentCmd.Flags().BoolVarP(&verboseMode, "verbose", "v", false,
		"Verbose output (same as --format verbose)")

	startCmd.AddCommand(startAgentCmd)
	startCmd.AddCommand(startGatewayCmd)
	rootCmd.AddCommand(startCmd)
}
