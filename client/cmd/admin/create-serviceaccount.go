package admin

import (
	"fmt"

	"github.com/hoophq/hoop/client/cmd/styles"
	"github.com/hoophq/hoop/common/log"
	"github.com/spf13/cobra"
)

var (
	svcAccountDisableFlag   bool
	svcAccountGroupsFlag    []string
	svcAccountNameFlag      string
	svcAccountOverwriteFlag bool
)

func init() {
	createSvcAccountCmd.Flags().BoolVar(&svcAccountOverwriteFlag, "overwrite", false, "It will create or update it if a service account already exists")
	createSvcAccountCmd.Flags().StringSliceVar(&svcAccountGroupsFlag, "groups", []string{}, "The list of groups this service account belongs to, e.g.: admin,devops,...")
	createSvcAccountCmd.Flags().StringVar(&svcAccountNameFlag, "name", "", "The display name of the service account")
	createSvcAccountCmd.Flags().BoolVar(&svcAccountDisableFlag, "disable", false, "Disable the service account")
}

var createSvcAccountCmd = &cobra.Command{
	Use:     "serviceaccount SUBJECT",
	Aliases: []string{"sa"},
	Short:   "Create a service account resource.",
	PreRun: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Usage()
			styles.PrintErrorAndExit("missing resource name")
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		resourceName := args[0]
		actionName := "created"
		method := "POST"
		resourceArgs := []string{"serviceaccounts"}
		if svcAccountOverwriteFlag {
			log.Debugf("service account %v exists, updating", resourceName)
			actionName = "updated"
			method = "PUT"
			resourceArgs = append(resourceArgs, resourceName)
		}
		apir := parseResourceOrDie(resourceArgs, method, outputFlag)
		status := "active"
		if svcAccountDisableFlag {
			status = "inactive"
		}
		resp, err := httpBodyRequest(apir, method, map[string]any{
			"subject": resourceName,
			"name":    svcAccountNameFlag,
			"groups":  svcAccountGroupsFlag,
			"status":  status,
		})
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
		}
		if apir.decodeTo == "raw" {
			jsonData, _ := resp.([]byte)
			fmt.Println(string(jsonData))
			return
		}
		fmt.Printf("service account %v %v\n", resourceName, actionName)
	},
}
