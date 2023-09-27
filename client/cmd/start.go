package cmd

import (
	"fmt"
	"os"

	"github.com/go-co-op/gocron"
	"github.com/runopsio/hoop/agent"
	"github.com/runopsio/hoop/common/monitoring"
	"github.com/runopsio/hoop/gateway"
	"github.com/runopsio/hoop/gateway/jobs"
	jobsessions "github.com/runopsio/hoop/gateway/jobs/sessions"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:          "start",
	Short:        "Runs hoop start agent or hoop start gateway",
	SilenceUsage: false,
	PreRun:       monitoring.SentryPreRun,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Runs hoop start agent or hoop start gateway")
	},
}

var startAgentCmd = &cobra.Command{
	Use:          "agent",
	Short:        "Runs the agent component",
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		agent.Run()
	},
}

var listenAdminAddr string

var startGatewayCmd = &cobra.Command{
	Use:          "gateway",
	Short:        "Runs the gateway component",
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		gateway.Run(listenAdminAddr)
	},
}

var jobsLongDesc = `Runs ad-hoc jobs specifying its job name.
When no argument is provided, it will run as a cronjob.

Available jobs are:

* walsessions
`

var startGatewayJobsCmd = &cobra.Command{
	Use:          "jobs JOBNAME",
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
	startGatewayCmd.Flags().StringVar(&listenAdminAddr, "listen-admin-addr", "127.0.0.1:8099", "the address of the adminstrative api")
	startCmd.Flags().StringSliceVarP(&startEnvFlag, "env", "e", nil, "The environment variables to set when starting hoop")
	rootCmd.AddCommand(startCmd)
}
