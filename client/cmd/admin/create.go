package admin

import (
	"github.com/spf13/cobra"
)

func init() {
	createCmd.AddCommand(createAgentCmd)
	createCmd.AddCommand(createOrgKeyCmd)
	createCmd.AddCommand(createConnectionCmd)
	createCmd.AddCommand(createPluginCmd)
	createCmd.AddCommand(createUserCmd)
	createCmd.AddCommand(createSvcAccountCmd)
	createCmd.AddCommand(createSvixEventTypeCmd)
	createCmd.AddCommand(createSvixEndppointCmd)
	createCmd.AddCommand(createSvixMessageCmd)
	createCmd.PersistentFlags().StringVarP(&outputFlag, "output", "o", "", "Output format. One off: (json)")
}

var createCmd = &cobra.Command{
	Use:   "create RESOURCE",
	Short: "Create resources",
}
