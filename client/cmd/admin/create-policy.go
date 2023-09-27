package admin

import (
	"fmt"

	"github.com/runopsio/hoop/client/cmd/styles"
	clientconfig "github.com/runopsio/hoop/client/config"
	"github.com/spf13/cobra"
)

var (
	policyConfigFlag      []string
	policyOverwriteFlag   bool
	policyConnectionsFlag []string
)

func init() {
	createPolicyCmd.Flags().StringSliceVarP(&policyConfigFlag, "config", "c", nil, "The configuration of the policy")
	createPolicyCmd.Flags().StringSliceVar(&policyConnectionsFlag, "connections", nil, "The connections that will be associated with the policy")
	createPolicyCmd.Flags().BoolVar(&policyOverwriteFlag, "overwrite", false, "It will create or update it if a policy already exists")
}

var policyLongDesc = `Create a policy resource. Available ones:

* datamasking
* accesscontrol
`

var createPolicyCmd = &cobra.Command{
	Use:     "policy POLICY/NAME",
	Short:   "Create a policy resource.",
	Aliases: []string{"policies"},
	Long:    policyLongDesc,
	// Example: getExamplesDesc,
	PreRun: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Usage()
			styles.PrintErrorAndExit("missing resource name")
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		method := "POST"
		actionName := "created"
		apir := parseResourceOrDie(args, method, outputFlag)
		ids, err := listConnectionIDs(apir.conf, policyConnectionsFlag)
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
		}
		policyBody := map[string]any{
			"name":        apir.name,
			"type":        apir.resourceType,
			"connections": ids,
			"config":      policyConfigFlag,
		}
		resp, err := httpBodyRequest(apir, method, policyBody)
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
		}

		if apir.decodeTo == "raw" {
			jsonData, _ := resp.([]byte)
			fmt.Println(string(jsonData))
			return
		}
		fmt.Printf("policy %s/%s %v\n", apir.resourceType, apir.name, actionName)
	},
}

func listConnectionIDs(conf *clientconfig.Config, inputConnectionNames []string) ([]string, error) {
	data, _, err := httpRequest(&apiResource{suffixEndpoint: "/api/connections", conf: conf, decodeTo: "list"})
	if err != nil {
		return nil, err
	}
	connectionList, ok := data.([]map[string]any)
	if !ok {
		return nil, fmt.Errorf("unknown connection list content, type=%T", data)
	}
	connectionMap := map[string]string{}
	for _, conn := range connectionList {
		connName := fmt.Sprintf("%v", conn["name"])
		connID := fmt.Sprintf("%v", conn["id"])
		connectionMap[connName] = connID
	}

	var connectionIDList []string
	var notFound []string
	for _, name := range inputConnectionNames {
		if connID, ok := connectionMap[name]; ok {
			connectionIDList = append(connectionIDList, connID)
			continue
		}
		notFound = append(notFound, name)
	}
	if len(notFound) > 0 {
		return nil, fmt.Errorf("connection(s) not found %q", notFound)
	}
	return connectionIDList, nil
}
