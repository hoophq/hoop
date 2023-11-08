package admin

import (
	"fmt"
	"strings"

	"github.com/runopsio/hoop/client/cmd/styles"
	clientconfig "github.com/runopsio/hoop/client/config"
	"github.com/runopsio/hoop/common/log"
	"github.com/spf13/cobra"
)

var (
	userDisableFlag   bool
	userGroupsFlag    []string
	userNameFlag      string
	userSlackIDFlag   string
	userOverwriteFlag bool
)

func init() {
	createUserCmd.Flags().BoolVar(&userOverwriteFlag, "overwrite", false, "It will create or update if a user already exists")
	createUserCmd.Flags().StringSliceVar(&userGroupsFlag, "groups", []string{}, "The list of groups this user belongs to, e.g.: admin,devops,...")
	createUserCmd.Flags().StringVar(&userNameFlag, "name", "", "The display name of the user")
	createUserCmd.Flags().StringVar(&userSlackIDFlag, "slackid", "", "The slack id of the user, only useful with slack plugin")
	createUserCmd.Flags().BoolVar(&userDisableFlag, "disable", false, "Disable the user")
}

var createUserCmd = &cobra.Command{
	Use:     "user NAME",
	Aliases: []string{"users"},
	Short:   "Create a user resource.",
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
		userID, err := getUser(clientconfig.GetClientConfigOrDie(), resourceName)
		if err != nil {
			styles.PrintErrorAndExit("failed retrieving user %v, %v", resourceName, err)
		}
		if userID != "" && userOverwriteFlag {
			log.Debugf("user %v/%v exists, updating", userID, resourceName)
			actionName = "updated"
			method = "PUT"
		}
		resourceArgs := []string{"users", userID}
		apir := parseResourceOrDie(resourceArgs, method, outputFlag)
		status := "active"
		if userDisableFlag {
			status = "inactive"
		}
		resp, err := httpBodyRequest(apir, method, map[string]any{
			// immutable fields
			"email": resourceName,
			"name":  userNameFlag,
			// updatable fields
			"groups":   userGroupsFlag,
			"slack_id": userSlackIDFlag,
			"status":   status,
		})
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
		}
		if apir.decodeTo == "raw" {
			jsonData, _ := resp.([]byte)
			fmt.Println(string(jsonData))
			return
		}
		fmt.Printf("user %v %v\n", resourceName, actionName)
	},
}

func getUser(conf *clientconfig.Config, userName string) (string, error) {
	resp, _, err := httpRequest(&apiResource{
		suffixEndpoint: fmt.Sprintf("/api/users/%v", userName),
		method:         "GET",
		conf:           conf,
		decodeTo:       "object"})
	if err != nil {
		if strings.Contains(err.Error(), "status=404") {
			return "", nil
		}
		return "", err
	}

	if obj, ok := resp.(map[string]any); ok {
		return fmt.Sprintf("%v", obj["id"]), nil
	}
	return "", fmt.Errorf("failed decoding response to object")
}
