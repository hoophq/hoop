package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/runopsio/hoop/client/cmd/styles"
	clientconfig "github.com/runopsio/hoop/client/config"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/spf13/cobra"
)

var (
	apiURLFlag  string
	grpcURLFlag string
	viewRawFlag bool
)

func init() {
	createCmd.Flags().StringVar(&apiURLFlag, "api-url", "", "The API URL to configure (required)")
	createCmd.Flags().StringVar(&grpcURLFlag, "grpc-url", "", "The gRPC URL to configure (optional)")
	viewCmd.Flags().BoolVar(&viewRawFlag, "raw", false, "Display sensitive credentials information")

	_ = createCmd.MarkFlagRequired("api-url")
	MainCmd.AddCommand(createCmd, viewCmd, clearCmd)
}

var MainCmd = &cobra.Command{
	Use:          "config",
	Short:        "Manage the hoop configuration file",
	Hidden:       false,
	SilenceUsage: false,
}

var createCmd = &cobra.Command{
	Use:          "create",
	Short:        "Creates or override a client hoop configuration file",
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		if grpcURLFlag != "" {
			if _, err := grpc.ParseServerAddress(grpcURLFlag); err != nil {
				styles.PrintErrorAndExit("--grpc-url value is not a gRPC address")
			}
		}

		apiURL := strings.TrimSuffix(apiURLFlag, "/")
		if _, err := grpc.ParseServerAddress(apiURL); err != nil {
			styles.PrintErrorAndExit("--api-url value is not an http address")
		}

		filepath, err := clientconfig.NewConfigFile(apiURL, grpcURLFlag, os.Getenv("HOOP_TOKEN"))
		if err != nil {
			styles.PrintErrorAndExit("failed creating configuration file, err=%v", err)
		}
		fmt.Printf("created %v\n", filepath)
	},
}

var viewCmd = &cobra.Command{
	Use:          "view",
	Short:        "Show the current configuration",
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		c := clientconfig.GetClientConfigOrDie()
		fmt.Printf("api_url=%s\n", c.ApiURL)
		fmt.Printf("grpc_url=%s\n", c.GrpcURL)
		if viewRawFlag {
			fmt.Printf("token=%s\n", c.Token)
		} else {
			fmt.Println("token=OMITTED")
		}
	},
}

var clearCmd = &cobra.Command{
	Use:          "clear",
	Short:        "Delete the client hoop configuration file if exists",
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		if err := clientconfig.Remove(); err != nil {
			styles.PrintErrorAndExit("failed removing configuration file, err=%v", err)
		}
		fmt.Println("configuration file removed")
	},
}
