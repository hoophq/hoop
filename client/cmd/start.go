package cmd

import (
	"fmt"
	"os"

	"github.com/go-co-op/gocron"
	"github.com/hoophq/hoop/agent"
	"github.com/hoophq/hoop/gateway"
	"github.com/hoophq/hoop/gateway/jobs"
	jobsessions "github.com/hoophq/hoop/gateway/jobs/sessions"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
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
	rootCmd.AddCommand(startCmd)
}
