package cmd

import (
	"fmt"
	"os"

	"github.com/go-co-op/gocron"
	"github.com/runopsio/hoop/agent"
	"github.com/runopsio/hoop/client/agentcontroller"
	"github.com/runopsio/hoop/gateway"
	"github.com/runopsio/hoop/gateway/jobs"
	jobsessions "github.com/runopsio/hoop/gateway/jobs/sessions"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
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

var startAgentControllerCmd = &cobra.Command{
	Use:          "agentcontroller",
	Short:        "Runs the agent controller component",
	SilenceUsage: false,
	Hidden:       true,
	Run: func(cmd *cobra.Command, args []string) {
		agentcontroller.RunServer()
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

var jobsLongDesc = `Runs ad-hoc jobs specifying its job name.
When no argument is provided, it will run as a cronjob.

Available jobs are:

* walsessions
`

var startGatewayJobsCmd = &cobra.Command{
	Use:          "jobs JOBNAME",
	Short:        "Start a job",
	Long:         jobsLongDesc,
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			jobs.Run()
			return
		}
		switch args[0] {
		case "walsessions":
			jobsessions.ProcessWalSessions(plugintypes.AuditPath, gocron.Job{})
		default:
			fmt.Printf("ad-hoc job %v not found\n", args[0])
			os.Exit(1)
		}
	},
}

func init() {
	startGatewayCmd.AddCommand(startGatewayJobsCmd)
	startCmd.AddCommand(startAgentCmd)
	startCmd.AddCommand(startGatewayCmd)
	startCmd.AddCommand(startAgentControllerCmd)
	rootCmd.AddCommand(startCmd)
}
