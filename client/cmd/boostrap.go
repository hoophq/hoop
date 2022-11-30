package cmd

import (
	"fmt"
	"os"

	"github.com/runopsio/hoop/client/k8s"
	"github.com/spf13/cobra"
)

var tokenGranterOptions = &k8s.TokenGranterOptions{}

var bootstrapCmd = &cobra.Command{
	Use:          "bootstrap",
	Short:        "Bootstrap resources on platforms",
	SilenceUsage: false,
}

var k8sCmd = &cobra.Command{
	Use:          "k8s",
	Short:        "Boostrap commands for Kubernetes clusters",
	SilenceUsage: false,
}

var tokenGranterCmd = &cobra.Command{
	Use:          "token-granter",
	Short:        "Setup an access token for managing tokens on Kubernetes",
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		kubeconfig, err := k8s.BootstrapTokenGranter(&k8s.TokenGranterOptions{})
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		fmt.Println(kubeconfig)
	},
}

func init() {
	tokenGranterCmd.Flags().StringVarP(&tokenGranterOptions.KubeconfigContext, "context", "c", "", "Context of kubeconfig to use when bootstraping resources, defaults to the current context")
	tokenGranterCmd.Flags().StringVarP(&tokenGranterOptions.Namespace, "namespace", "n", k8s.DefaultNamespaceName, "The name of the namespace to provision all resources")
	tokenGranterCmd.Flags().StringVar(&tokenGranterOptions.ClusterRole, "clusterrole", k8s.DefaultClusterRole, "The name of the cluster role which will be bound to the token")
	tokenGranterCmd.Flags().DurationVar(&tokenGranterOptions.Expiration, "duration", 0, "Requested lifetime of the issued token. The server may return a token with a longer or shorter lifetime.")

	bootstrapCmd.AddCommand(k8sCmd)
	k8sCmd.AddCommand(tokenGranterCmd)
	rootCmd.AddCommand(bootstrapCmd)
}
