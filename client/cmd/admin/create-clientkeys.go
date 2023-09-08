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
	clientKeysActiveFlag    string
	clientKeysOverwriteFlag bool
)

func init() {
	createClientKeysCmd.Flags().StringVar(&clientKeysActiveFlag, "active", "true", "To activate (true) or disable (false) the key")
	createClientKeysCmd.Flags().BoolVar(&clientKeysOverwriteFlag, "overwrite", false, "It will create or update it if the resource already exists")
	_ = createClientKeysCmd.MarkFlagRequired("mode")
}

var createClientKeysCmd = &cobra.Command{
	Use:   "clientkeys NAME",
	Short: "Create a client key resource.",
	PreRun: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Usage()
			styles.PrintErrorAndExit("missing resource name")
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		resourceArgs := []string{"clientkeys", args[0]}
		method := "POST"
		config := clientconfig.GetClientConfigOrDie()
		if clientKeysOverwriteFlag {
			exists, err := getClientKey(config, args[0])
			if err != nil {
				styles.PrintErrorAndExit("failed retrieving connection %v, %v", args[0], err)
			}
			if exists {
				log.Debugf("clientkey %v exists, update it", args[0])
				method = "PUT"
			}
		}
		apir := parseResourceOrDie(resourceArgs, method, outputFlag)
		reqBody := map[string]any{
			"name":   apir.name,
			"active": clientKeysActiveFlag == "true",
		}
		resp, err := httpBodyRequest(apir, method, reqBody)
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
		}
		if apir.decodeTo == "raw" {
			jsonData, _ := resp.([]byte)
			fmt.Println(string(jsonData))
			return
		}
		respMap, ok := resp.(map[string]any)
		if !ok {
			styles.PrintErrorAndExit("failed decoding response map")
		}
		if method == "POST" {
			fmt.Printf("%v\n", respMap["dsn"])
			return
		}
		fmt.Printf("clientkey %v updated\n", apir.name)
	},
}

func getClientKey(conf *clientconfig.Config, name string) (bool, error) {
	resp, _, err := httpRequest(&apiResource{
		suffixEndpoint: fmt.Sprintf("/api/clientkeys/%v", name),
		method:         "GET",
		conf:           conf,
		decodeTo:       "object"})
	if err != nil {
		if strings.Contains(err.Error(), "status=404") {
			return false, nil
		}
		return false, err
	}
	if _, ok := resp.(map[string]any); !ok {
		return false, fmt.Errorf("failed decoding response to object")
	}
	return true, nil
}
