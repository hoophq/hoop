package admin

import (
	"fmt"

	"github.com/hoophq/hoop/client/cmd/styles"
	"github.com/spf13/cobra"
)

var deleteLongDesc = `Delete resource by its name. Available ones:

* agent
* connection
* users
`

var deleteCmd = &cobra.Command{
	Use:   "delete TYPE/NAME",
	Short: "Delete resources by name",
	Long:  deleteLongDesc,
	PreRun: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			styles.PrintErrorAndExit("missing resource: type/name")
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		apir := parseResourceOrDie(args, "DELETE", "")
		if err := httpDeleteRequest(apir); err != nil {
			styles.PrintErrorAndExit(err.Error())
		}
		if apir.name != "" {
			fmt.Printf("%s %q deleted\n", apir.resourceType, apir.name)
			return
		}
		fmt.Printf("%s deleted\n", apir.resourceType)
	},
}
