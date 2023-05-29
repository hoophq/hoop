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
	createCmd.Flags().StringVar(&apiURLFlag, "api-url", "", "The API URL to configure")
	createCmd.Flags().StringVar(&grpcURLFlag, "grpc-url", "", "The gRPC URL to configure")
	createCmd.MarkFlagRequired("api-url")
	createCmd.MarkFlagRequired("grpc-url")

	viewCmd.Flags().BoolVar(&viewRawFlag, "raw", false, "Display sensitive credentials information")

	MainCmd.AddCommand(createCmd, viewCmd, clearCmd)
}

var MainCmd = &cobra.Command{
	Use:          "config",
	Short:        "It manages the hoop configuration file",
	Hidden:       true,
	SilenceUsage: false,
}

var createCmd = &cobra.Command{
	Use:          "create",
	Short:        "Creates or override a client hoop configuration file",
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		accessToken := os.Getenv("HOOP_TOKEN")
		if accessToken == "" {
			styles.PrintErrorAndExit("missing HOOP_TOKEN environment variable")
		}
		if _, err := grpc.ParseServerAddress(grpcURLFlag); err != nil {
			styles.PrintErrorAndExit("--grpc-url value is not a gRPC address")
		}

		if _, err := grpc.ParseServerAddress(apiURLFlag); err != nil {
			styles.PrintErrorAndExit("--api-url value is not an http address")
		}

		if len(strings.Split(accessToken, ".")) != 3 {
			styles.PrintErrorAndExit("access token is not a jwt token")
		}

		filepath, err := clientconfig.NewConfigFile(apiURLFlag, grpcURLFlag, accessToken)
		if err != nil {
			styles.PrintErrorAndExit("failed creating configuration file, err=%v", err)
		}
		fmt.Printf("created %v\n", filepath)
	},
}

var viewCmd = &cobra.Command{
	Use:          "view",
	Short:        "Show the client hoop configuration file",
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
