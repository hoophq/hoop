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
		// args = []string{"policies", args[0]}
		// TODO: check if the policy exists before creating ...
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

// func getPolicyByName(apir *apiResource, policyType, policyName string) (any, error) {
// 	data, _, err := httpRequest(&apiResource{suffixEndpoint: "/api/policies", conf: apir.conf, decodeTo: "list"})
// 	if err != nil {
// 		return nil, err
// 	}
// 	contents, ok := data.([]map[string]any)
// 	if !ok {
// 		return nil, fmt.Errorf("content type mismatch")
// 	}
// 	fmt.Printf("CONTENTS: %v\n", contents)

// 	return nil, nil
// }

// func parsePluginConfig() (map[string]any, error) {
// 	envVar := map[string]any{}
// 	var invalidEnvs []string
// 	for _, envvarStr := range pluginConfigFlag {
// 		key, val, found := strings.Cut(envvarStr, "=")
// 		if !found {
// 			invalidEnvs = append(invalidEnvs, envvarStr)
// 			continue
// 		}
// 		key = strings.TrimSpace(key)
// 		val = strings.TrimSpace(val)
// 		if strings.HasPrefix(val, "path:") {
// 			pathFile := val[5:]
// 			configData, err := os.ReadFile(pathFile)
// 			if err != nil {
// 				return nil, fmt.Errorf("failed reading config data, path=%v, err=%v", pathFile, err)
// 			}
// 			val = string(configData)
// 		}
// 		envVar[key] = base64.StdEncoding.EncodeToString([]byte(val))
// 	}
// 	if len(invalidEnvs) > 0 {
// 		return nil, fmt.Errorf("invalid plugin config, expected key=val. found=%v", invalidEnvs)
// 	}
// 	return envVar, nil
// }

// func parsePluginConnections(conf *clientconfig.Config) ([]map[string]any, error) {
// 	connectionConfig := []map[string]any{}
// 	connectionMap, err := listConnectionNames(conf)
// 	if err != nil {
// 		return nil, err
// 	}
// 	for _, connectionOption := range pluginConnectionFlag {
// 		connectionName, configStr, found := strings.Cut(connectionOption, ":")
// 		if !found && connectionName == "" {
// 			return nil, fmt.Errorf(`wrong format for connection %q, expected "<conn>:<config01>;config02>;..."`,
// 				connectionOption)
// 		}
// 		connectionName = strings.TrimSpace(connectionName)
// 		var connConfig []string
// 		for _, c := range strings.Split(configStr, ";") {
// 			c = strings.TrimSpace(c)
// 			if c == "" {
// 				continue
// 			}
// 			connConfig = append(connConfig, c)
// 		}
// 		connID, ok := connectionMap[connectionName]
// 		if !ok {
// 			return nil, fmt.Errorf("connection %q not found", connectionName)
// 		}
// 		connectionConfig = append(connectionConfig, map[string]any{
// 			"id":     connID,
// 			"config": connConfig,
// 		})
// 	}
// 	return connectionConfig, nil
// }

// func listConnectionNames(conf *clientconfig.Config) (map[string]string, error) {
// 	resp, _, err := httpRequest(&apiResource{
// 		suffixEndpoint: "/api/connections",
// 		method:         "GET",
// 		conf:           conf,
// 		decodeTo:       "list"})
// 	if err != nil {
// 		return nil, err
// 	}

// 	itemList, ok := resp.([]map[string]any)
// 	if !ok {
// 		return nil, fmt.Errorf("failed decoding response to object, type=%T", resp)
// 	}
// 	connectionNames := map[string]string{}
// 	for _, item := range itemList {
// 		connectionNames[fmt.Sprintf("%v", item["name"])] = fmt.Sprintf("%v", item["id"])
// 	}
// 	return connectionNames, nil
// }
