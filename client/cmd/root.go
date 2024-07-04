package cmd

import (
	"os"

	"github.com/runopsio/hoop/client/cmd/admin"
	"github.com/runopsio/hoop/client/cmd/config"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
	"github.com/spf13/cobra"
)

var (
	debugGrpcFlag bool
	debugFlag     bool
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:               "hoop",
	CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	Long: `Connect to private infra-structure without the need of a VPN.
https://hoop.dev/docs`,
	PersistentPreRun: func(_ *cobra.Command, _ []string) {
		// run with the env GODEBUG=http2debug=2 to log http2 frames.
		if debugGrpcFlag {
			log.SetGrpcLogger()
		}
		if debugFlag {
			log.SetDefaultLoggerLevel(log.LevelDebug)
		}
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&debugGrpcFlag, "debug-grpc", grpc.ShouldDebugGrpc(), "Turn on debugging of gRPC (http2) if applicable")
	rootCmd.PersistentFlags().BoolVar(&debugFlag, "debug", false, "Turn on debugging")

	rootCmd.AddCommand(config.MainCmd)
	rootCmd.AddCommand(admin.MainCmd)
}
