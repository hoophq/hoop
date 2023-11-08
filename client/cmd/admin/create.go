package admin

import (
	"github.com/spf13/cobra"
)

func init() {
	createCmd.AddCommand(createAgentCmd)
	createCmd.AddCommand(createConnectionCmd)
	createCmd.AddCommand(createPluginCmd)
	createCmd.AddCommand(createUserCmd)
	createCmd.AddCommand(createClientKeysCmd)
	createCmd.AddCommand(createPolicyCmd)
	createCmd.AddCommand(createSvcAccountCmd)
	createCmd.PersistentFlags().StringVarP(&outputFlag, "output", "o", "", "Output format. One off: (json)")
}

var createCmd = &cobra.Command{
	Use:   "create RESOURCE",
	Short: "Create resources",
}
