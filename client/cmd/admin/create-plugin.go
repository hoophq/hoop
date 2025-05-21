package admin

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/common/log"
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
	createPluginCmd.Flags().StringSliceVarP(&pluginConfigFlag, "config", "c", nil, "The configuration of the plugin. Values could be raw values, base64://<b64-content> or file:///path/to/file ")
	createPluginCmd.Flags().StringSliceVar(&pluginConnectionFlag, "connection", nil, "The connection to associate with the plugin in the form of '<conn>:<config01>;<config02>;...'")
	createPluginCmd.Flags().BoolVar(&pluginOverwriteFlag, "overwrite", false, "It will create or update it if a plugin already exists")
}

var createPluginCmd = &cobra.Command{
	Use:     "plugin NAME",
	Short:   "Create a plugin resource.",
	Aliases: []string{"plugins"},
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
			pl, err := getPlugin(clientconfig.GetClientConfigOrDie(), args[1])
			if err != nil {
				styles.PrintErrorAndExit("failed retrieving plugin %q, %v", args[1], err)
			}
			if pl != nil {
				log.Debugf("plugin %v exists, update it", args[1])
				actionName = "updated"
				method = "PUT"
			}
		}
		apir := parseResourceOrDie(args, method, outputFlag)
		pluginEnvVars, err := parsePluginConfig()
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
		}
		pluginConnections, err := parsePluginConnections(apir.conf)
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

func getPlugin(conf *clientconfig.Config, pluginName string) (map[string]any, error) {
	resp, _, err := httpRequest(&apiResource{
		suffixEndpoint: fmt.Sprintf("/api/plugins/%v", pluginName),
		method:         "GET",
		conf:           conf,
		decodeTo:       "object"})
	if err != nil {
		if strings.Contains(err.Error(), "status=404") {
			return nil, nil
		}
		return nil, err
	}
	data, ok := resp.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("failed decoding response to object")
	}
	return data, nil
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
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val, err := getEnvValue(val)
		if err != nil {
			return nil, fmt.Errorf("unable to get value: %v", err)
		}
		envVar[key] = base64.StdEncoding.EncodeToString([]byte(val))
	}
	if len(invalidEnvs) > 0 {
		return nil, fmt.Errorf("invalid plugin config, expected key=val. found=%v", invalidEnvs)
	}
	return envVar, nil
}

func parsePluginConnections(conf *clientconfig.Config) ([]map[string]any, error) {
	connectionConfig := []map[string]any{}
	connectionMap, err := listConnectionNames(conf)
	if err != nil {
		return nil, err
	}
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
		connID, ok := connectionMap[connectionName]
		if !ok {
			return nil, fmt.Errorf("connection %q not found", connectionName)
		}
		connectionConfig = append(connectionConfig, map[string]any{
			"id":     connID,
			"config": connConfig,
		})
	}
	return connectionConfig, nil
}

func listConnectionNames(conf *clientconfig.Config) (map[string]string, error) {
	resp, _, err := httpRequest(&apiResource{
		suffixEndpoint: "/api/connections",
		method:         "GET",
		conf:           conf,
		decodeTo:       "list"})
	if err != nil {
		return nil, err
	}

	itemList, ok := resp.([]map[string]any)
	if !ok {
		return nil, fmt.Errorf("failed decoding response to object, type=%T", resp)
	}
	connectionNames := map[string]string{}
	for _, item := range itemList {
		connectionNames[fmt.Sprintf("%v", item["name"])] = fmt.Sprintf("%v", item["id"])
	}
	return connectionNames, nil
}
