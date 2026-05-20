package cmd

import (
	"fmt"
	"os"

	"github.com/hoophq/hoop/client/cmd/admin"
	"github.com/hoophq/hoop/client/cmd/config"
	"github.com/hoophq/hoop/client/cmd/runbooks"
	"github.com/hoophq/hoop/client/cmd/styles"
	"github.com/hoophq/hoop/client/cmd/upgrade"
	"github.com/hoophq/hoop/client/cmd/versions"
	clientupgrade "github.com/hoophq/hoop/client/upgrade"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/httpclient"
	"github.com/hoophq/hoop/common/log"
	"github.com/spf13/cobra"
)

var (
	debugGrpcFlag     bool
	debugFlag         bool
	skipTLSVerifyFlag bool
)

var rootPreRunFn = func(_ *cobra.Command, _ []string) {
	// run with the env GODEBUG=http2debug=2 to log http2 frames.
	if debugGrpcFlag {
		log.SetGrpcLogger()
	}
	if debugFlag {
		log.SetDefaultLoggerLevel(log.LevelDebug)
	}
}

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:               "hoop",
	CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	Long: `Connect to private infra-structure without the need of a VPN.
https://hoop.dev/docs`,
	PersistentPreRun: rootPreRunFn,
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
	// Warn (once) when the gateway and local CLI versions disagree.
	// Registered before any HTTP client is built so every call hits it.
	httpclient.VersionCheckCallback = clientupgrade.WarnOnceFromServerHeader

	rootCmd.PersistentFlags().BoolVar(&debugGrpcFlag, "debug-grpc", grpc.ShouldDebugGrpc(), "Turn on debugging of gRPC (http2) if applicable")
	rootCmd.PersistentFlags().BoolVar(&debugFlag, "debug", false, "Turn on debugging")
	rootCmd.PersistentFlags().BoolVar(&skipTLSVerifyFlag, "skip-tls-verify", false, "Skip TLS verification when connecting to Hoop components")

	rootCmd.AddCommand(runbooks.MainCmd)
	rootCmd.AddCommand(config.MainCmd)
	rootCmd.AddCommand(admin.MainCmd)
	rootCmd.AddCommand(upgrade.MainCmd)
	rootCmd.AddCommand(versions.MainCmd)
	rootCmd.AddCommand(claudeCmd)
}

func printErrorAndExit(format string, v ...any) {
	errOutput := styles.ClientError(fmt.Sprintf(format, v...))
	fmt.Println(errOutput)
	os.Exit(1)
}
