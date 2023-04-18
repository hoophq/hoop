package admin

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/runopsio/hoop/client/cmd/styles"
	clientconfig "github.com/runopsio/hoop/client/config"
	"github.com/runopsio/hoop/common/log"
	"github.com/spf13/cobra"
)

var (
	pluginSourceFlag     string
	pluginPriorityFlag   int
	pluginConfigFlag     []string
	pluginConnectionFlag []string
	pluginOverwriteFlag  bool
)

func init() {
	createPluginCmd.Flags().StringVar(&pluginSourceFlag, "source", "", "The source to get plugins from. One off: ('<org>/<pluginname>', 'path:/path/to/plugin/folder')")
	createPluginCmd.Flags().IntVar(&pluginPriorityFlag, "priority", 0, "The priority of the plugin, a greater value means it will execute it first.")
	createPluginCmd.Flags().StringSliceVarP(&pluginConfigFlag, "config", "c", nil, "The configuration of the plugin")
	createPluginCmd.Flags().StringSliceVar(&pluginConnectionFlag, "connection", nil, "The connection to associate with the plugin in the form of 'conn:config01;config02;...'")
	createPluginCmd.Flags().BoolVar(&pluginOverwriteFlag, "overwrite", false, "It will create or update it if a plugin already exists")

}

var createPluginCmd = &cobra.Command{
	Use:   "plugin NAME",
	Short: "Create a plugin resource.",
	PreRun: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Usage()
			styles.PrintErrorAndExit("missing resource name")
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		args = []string{"plugins", args[0]}
		method := "POST"
		actionName := "created"
		if pluginOverwriteFlag {
			exists, err := getPlugin(clientconfig.GetClientConfigOrDie(), args[1])
			if err != nil {
				styles.PrintErrorAndExit("failed retrieving plugin %q, %v", args[1], err)
			}
			if exists {
				log.Debugf("plugin %v exists, update it", args[0])
				actionName = "updated"
				method = "PUT"
			}
		}
		apir := parseResourceOrDie(args, method, outputFlag)
		pluginEnvVars, err := parsePluginConfig()
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
		}
		pluginConnections, err := parsePluginConnections()
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
		}

		var pluginSource *string
		if pluginSourceFlag != "" {
			pluginSource = &pluginSourceFlag
		}

		pluginBody := map[string]any{
			"name":        apir.name,
			"priority":    pluginPriorityFlag,
			"source":      pluginSource,
			"connections": pluginConnections,
		}
		resp, err := httpBodyRequest(apir, method, pluginBody)
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
		}
		// if len(pluginEnvVars)
		if _, err = putConfig(apir.conf, apir.name, pluginEnvVars); err != nil {
			styles.PrintErrorAndExit(err.Error())
		}

		if apir.decodeTo == "raw" {
			jsonData, _ := resp.([]byte)
			fmt.Println(string(jsonData))
			return
		}
		fmt.Printf("plugin %v %v\n", apir.name, actionName)
	},
}

func putConfig(conf *clientconfig.Config, pluginName string, envVars map[string]any) (any, error) {
	return httpBodyRequest(&apiResource{
		suffixEndpoint: fmt.Sprintf("/api/plugins/%v/config", pluginName),
		method:         "PUT",
		conf:           conf,
		decodeTo:       "object"}, "PUT", envVars)
}

func getPlugin(conf *clientconfig.Config, pluginName string) (bool, error) {
	resp, err := httpRequest(&apiResource{
		suffixEndpoint: fmt.Sprintf("/api/plugins/%v", pluginName),
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

func parsePluginConfig() (map[string]any, error) {
	envVar := map[string]any{}
	var invalidEnvs []string
	for _, envvarStr := range pluginConfigFlag {
		key, val, found := strings.Cut(envvarStr, "=")
		if !found {
			invalidEnvs = append(invalidEnvs, envvarStr)
			continue
		}
		encodeType, configKey, found := strings.Cut(key, ":")
		if found {
			key = configKey
			if encodeType != "b64" {
				return nil, fmt.Errorf(`wrong encode type %v, accept only "b64"`, encodeType)
			}
		}

		key = strings.TrimSpace(strings.ToUpper(key))
		val = strings.TrimSpace(val)
		envVar[key] = base64.StdEncoding.EncodeToString([]byte(val))
		if strings.HasPrefix(encodeType, "b64") {
			envVar[key] = val
		}
	}
	if len(invalidEnvs) > 0 {
		return nil, fmt.Errorf("invalid plugin config, expected key=val. found=%v", invalidEnvs)
	}
	return envVar, nil
}

func parsePluginConnections() ([]map[string]any, error) {
	connectionConfig := []map[string]any{}
	for _, connectionOption := range pluginConnectionFlag {
		connectionName, configStr, found := strings.Cut(connectionOption, ":")
		if !found && connectionName == "" {
			return nil, fmt.Errorf(`wrong format for connection %q, expected "<conn>:<config01>;config02>;..."`,
				connectionOption)
		}
		connectionName = strings.TrimSpace(connectionName)
		var connConfig []string
		for _, c := range strings.Split(configStr, ";") {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			connConfig = append(connConfig, c)
		}
		connectionConfig = append(connectionConfig, map[string]any{
			"name":   connectionName,
			"config": connConfig,
			"groups": nil,
		})
	}
	return connectionConfig, nil
}
