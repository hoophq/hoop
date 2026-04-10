package cmd

import (
	"fmt"
	"strings"

	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/spf13/cobra"
)

var (
	agentTokenFlag  string
	agentAPIURLFlag string
	agentGRPCURLFlag string
)

var agentIdentityCmd = &cobra.Command{
	Use:   "agent-identity",
	Short: "Manage agent identity configuration",
}

var agentIdentityConfigureCmd = &cobra.Command{
	Use:          "configure",
	Short:        "Configure the CLI to authenticate as an agent identity",
	Long:         `Write a local configuration file using an agent identity token instead of an IDP login flow.`,
	SilenceUsage: true,
	Run: func(cmd *cobra.Command, args []string) {
		if !strings.HasPrefix(agentTokenFlag, "agt-") {
			styles.PrintErrorAndExit("--token must start with \"agt-\"")
		}

		apiURL := strings.TrimSuffix(agentAPIURLFlag, "/")
		if _, err := grpc.ParseServerAddress(apiURL); err != nil {
			styles.PrintErrorAndExit("--api-url value is not a valid HTTP address")
		}

		grpcURL := agentGRPCURLFlag
		if grpcURL == "" {
			si, err := fetchServerInfo(apiURL, agentTokenFlag, "")
			if err != nil {
				styles.PrintErrorAndExit("failed fetching gRPC URL from gateway: %v", err)
			}
			grpcURL = si.GrpcURL
		}

		if _, err := grpc.ParseServerAddress(grpcURL); err != nil {
			styles.PrintErrorAndExit("--grpc-url value is not a valid gRPC address")
		}

		filepath, err := clientconfig.NewConfigFile(apiURL, grpcURL, agentTokenFlag, "")
		if err != nil {
			styles.PrintErrorAndExit("failed writing configuration file: %v", err)
		}
		fmt.Printf("configuration saved to %v\n", filepath)
	},
}

func init() {
	agentIdentityConfigureCmd.Flags().StringVar(&agentTokenFlag, "token", "", "Agent identity token (agt-...) from the API")
	agentIdentityConfigureCmd.Flags().StringVar(&agentAPIURLFlag, "api-url", "", "The gateway API URL")
	agentIdentityConfigureCmd.Flags().StringVar(&agentGRPCURLFlag, "grpc-url", "", "The gateway gRPC URL (optional, fetched from the gateway if not set)")

	_ = agentIdentityConfigureCmd.MarkFlagRequired("token")
	_ = agentIdentityConfigureCmd.MarkFlagRequired("api-url")

	agentIdentityCmd.AddCommand(agentIdentityConfigureCmd)
	rootCmd.AddCommand(agentIdentityCmd)
}
