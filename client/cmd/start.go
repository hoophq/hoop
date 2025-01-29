package cmd

import (
	"github.com/hoophq/hoop/agent"
	"github.com/hoophq/hoop/gateway"
	"github.com/spf13/cobra"
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
	startCmd.AddCommand(startAgentCmd)
	startCmd.AddCommand(startGatewayCmd)
	rootCmd.AddCommand(startCmd)
}
