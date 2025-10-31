package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/spf13/cobra"
)

var (
	apiURLFlag  string
	grpcURLFlag string
	tlsCAFlag   string
	viewRawFlag bool
)

func init() {
	createCmd.Flags().StringVar(&apiURLFlag, "api-url", "", "The API URL to configure (required)")
	createCmd.Flags().StringVar(&grpcURLFlag, "grpc-url", "", "The gRPC URL to configure (optional)")
	createCmd.Flags().StringVar(&tlsCAFlag, "tls-ca", "", "The path to the TLS certificate authority to use (optional)")
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
		var tlsCA string
		if tlsCAFlag != "" {
			data, err := os.ReadFile(tlsCAFlag)
			if err != nil {
				styles.PrintErrorAndExit("failed reading --tls-ca file: %v", err)
			}
			tlsCA = string(data)
		}
		filepath, err := clientconfig.NewConfigFile(apiURL, grpcURLFlag, os.Getenv("HOOP_TOKEN"), tlsCA)
		if err != nil {
			styles.PrintErrorAndExit("failed creating configuration file, err=%v", err)
		}
		fmt.Printf("created %v\n", filepath)
	},
}

var viewCmd = &cobra.Command{
	Use:          "view [ATTRIBUTE]",
	Short:        "Show the current configuration",
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		c := clientconfig.GetClientConfigOrDie()
		if len(args) > 0 {
			var value string
			switch args[0] {
			case "api_url":
				value = c.ApiURL
			case "grpc_url":
				value = c.GrpcURL
			case "token":
				value = c.Token
			case "tls_ca":
				value = c.TlsCAB64Enc
			default:
				styles.PrintErrorAndExit("attribute not supported, accept only: api_url, grpc_url, token, tls_ca")
			}
			fmt.Println(value)
			return
		}

		fmt.Printf("api_url=%s\n", c.ApiURL)
		fmt.Printf("grpc_url=%s\n", c.GrpcURL)
		if viewRawFlag {
			fmt.Printf("token=%s\n", c.Token)
		} else {
			fmt.Println("token=OMITTED")
		}
		if tlsCA := c.TlsCAB64Enc; tlsCA != "" {
			fmt.Printf("tls_ca=%s\n", tlsCA)
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
