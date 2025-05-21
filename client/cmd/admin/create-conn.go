package admin

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/spf13/cobra"
)

var (
	connAgentFlag           string
	connPuginFlag           []string
	reviewersFlag           []string
	connRedactTypesFlag     []string
	connTypeFlag            string
	connTagsFlag            []string
	connSecretFlag          []string
	connAccessModesFlag     []string
	connSchemaFlag          string
	connGuardRailRules      []string
	connJiraIssueTemplateID string
	skipStrictValidation    bool
	connOverwriteFlag       bool

	defaultAccessModes = []string{"connect", "exec", "runbooks"}
)

func init() {
	createConnectionCmd.Flags().StringVarP(&connAgentFlag, "agent", "a", "", "Name of the agent")
	createConnectionCmd.Flags().StringVarP(&connTypeFlag, "type", "t", "custom", "Type of the connection. One off: (custom, application/[httpproxy|ssh|tcp], database/[mssql|mongodb|mysql|postgres])")
	createConnectionCmd.Flags().StringSliceVarP(&connPuginFlag, "plugin", "p", nil, "Plugins that will be enabled for this connection in the form of: <plugin>:<config01>;<config02>,...")
	createConnectionCmd.Flags().StringSliceVar(&reviewersFlag, "reviewers", nil, "The approval groups for this connection")
	createConnectionCmd.Flags().StringSliceVar(&connRedactTypesFlag, "redact-types", nil, "The redact types for this connection")
	createConnectionCmd.Flags().BoolVar(&connOverwriteFlag, "overwrite", false, "It will create or update it if a connection already exists")
	createConnectionCmd.Flags().BoolVar(&skipStrictValidation, "skip-validation", false, "It will skip any strict validation")
	createConnectionCmd.Flags().StringSliceVarP(&connSecretFlag, "env", "e", nil, "The environment variables of the connection, as KEY=VAL. Values could be raw values, base64://<b64-content> or file:///path/to/file")
	createConnectionCmd.Flags().StringSliceVar(&connTagsFlag, "tags", nil, "Tags to identify connections in a key=value format")
	createConnectionCmd.Flags().StringSliceVar(&connAccessModesFlag, "access-modes", defaultAccessModes, "Access modes enabled for this connection. Accepted values: [runbooks, exec, connect]")
	createConnectionCmd.Flags().StringVar(&connSchemaFlag, "schema", "", "Enable or disable the schema for this connection on the WebClient. Accepted values: [disabled, enabled]")
	createConnectionCmd.Flags().StringSliceVar(&connGuardRailRules, "guardrail-rules", nil, "The id of the guard rail rules for this connection")
	createConnectionCmd.Flags().StringVar(&connJiraIssueTemplateID, "jira-issue-template-id", "", "The id of the jira issue template to associate with this connection")
	createConnectionCmd.MarkFlagRequired("agent")
}

var createConnExamplesDesc = `
hoop admin create connection hello-hoop -a default -- bash -c 'echo hello hoop'
hoop admin create connection tcpsvc -a default -t application/tcp -e HOST=127.0.0.1 -e PORT=3000
`
var createConnectionCmd = &cobra.Command{
	Use:     "connection NAME [-- COMMAND]",
	Aliases: []string{"conn", "connections"},
	Example: createConnExamplesDesc,
	Short:   "Create a connection resource.",
	PreRun: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Usage()
			styles.PrintErrorAndExit("missing resource name")
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		resourceArgs := []string{"connection", args[0]}
		actionName := "created"
		method := "POST"
		config := clientconfig.GetClientConfigOrDie()
		if connOverwriteFlag {
			exists, err := getConnection(config, args[0])
			if err != nil {
				styles.PrintErrorAndExit("failed retrieving connection %v, %v", args[0], err)
			}
			if exists {
				log.Debugf("connection %v exists, update it", args[0])
				actionName = "updated"
				method = "PUT"
			}
		}
		apir := parseResourceOrDie(resourceArgs, method, outputFlag)
		cmdList := []string{}
		if len(args) > 1 {
			for _, cmdparam := range args[1:] {
				// allows passing \t literal when executing the command
				cmdparam := strings.ReplaceAll(cmdparam, "\\t", "\t")
				cmdList = append(cmdList, cmdparam)
			}
		}
		apir.name = NormalizeResourceName(apir.name)
		envVar, err := parseEnvPerType()
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
		}
		connType, subType, _ := strings.Cut(connTypeFlag, "/")
		protocolConnectionType := pb.ToConnectionType(connType, subType)
		if !skipStrictValidation {
			switch protocolConnectionType {
			case pb.ConnectionTypeCommandLine:
				if len(cmdList) == 0 {
					styles.PrintErrorAndExit("command-line type must be at least one command")
				}
			case pb.ConnectionTypeTCP:
				if err := validateTcpEnvs(envVar); err != nil {
					styles.PrintErrorAndExit(err.Error())
				}
			case pb.ConnectionTypePostgres, pb.ConnectionTypeMySQL, pb.ConnectionTypeMSSQL:
				if err := validateNativeDbEnvs(envVar); err != nil {
					styles.PrintErrorAndExit(err.Error())
				}
			case pb.ConnectionTypeMongoDB:
				if envVar["envvar:CONNECTION_STRING"] == "" {
					styles.PrintErrorAndExit("missing required CONNECTION_STRING env for %v", pb.ConnectionTypeMongoDB)
				}
			case pb.ConnectionTypeHttpProxy:
				if envVar["envvar:REMOTE_URL"] == "" {
					styles.PrintErrorAndExit("missing required REMOTE_URL env for %v", pb.ConnectionTypeHttpProxy)
				}
			case pb.ConnectionTypeSSH:
				if err := validateSSHEnvs(envVar); err != nil {
					styles.PrintErrorAndExit(err.Error())
				}
			default:
				styles.PrintErrorAndExit("invalid connection type %q", connType)
			}
		}

		agentID, err := getAgentIDByName(apir.conf, connAgentFlag)
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
		}
		if agentID == "" && !skipStrictValidation {
			styles.PrintErrorAndExit("could not find agent by name %q", connAgentFlag)
		}

		connectionTags := map[string]string{}
		for _, keyValTag := range connTagsFlag {
			key, val, found := strings.Cut(keyValTag, "=")
			if !found {
				continue
			}
			connectionTags[key] = val
		}

		connectionBody := map[string]any{
			"name":                   apir.name,
			"type":                   connType,
			"subtype":                subType,
			"command":                cmdList,
			"secret":                 envVar,
			"agent_id":               agentID,
			"reviewers":              reviewersFlag,
			"redact_enabled":         true,
			"redact_types":           connRedactTypesFlag,
			"connection_tags":        connectionTags,
			"guardrail_rules":        connGuardRailRules,
			"jira_issue_template_id": connJiraIssueTemplateID,
			"access_mode_runbooks":   verifyAccessModeStatus("runbooks"),
			"access_mode_exec":       verifyAccessModeStatus("exec"),
			"access_mode_connect":    verifyAccessModeStatus("connect"),
			"access_schema":          verifySchemaStatus(connSchemaFlag, connType),
		}

		resp, err := httpBodyRequest(apir, method, connectionBody)
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
		}

		if apir.decodeTo == "raw" && len(connPuginFlag) == 0 {
			jsonData, _ := resp.([]byte)
			fmt.Println(string(jsonData))
			return
		}

		fmt.Printf("connection %v %v\n", apir.name, actionName)

		var connectionID string
		if data, ok := resp.(map[string]any); ok {
			connectionID = fmt.Sprintf("%v", data["id"])
		}
		if connectionID == "" && len(connPuginFlag) > 0 {
			fmt.Println("missing id when creating/updating connection, it will not configure the plugin")
			return
		}

		pluginList, err := parseConnectionPlugins(config, args[0], connectionID)
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
		}

		var plugins []string
		for _, pluginData := range pluginList {
			_, err := httpBodyRequest(&apiResource{
				suffixEndpoint: fmt.Sprintf("/api/plugins/%v", pluginData["name"]),
				method:         "PUT",
				conf:           config,
				decodeTo:       "object"}, "PUT", pluginData)
			if err != nil {
				if len(plugins) > 0 {
					fmt.Printf("plugin(s) %v updated\n", plugins)
				}
				styles.PrintErrorAndExit(err.Error())
			}
			plugins = append(plugins, fmt.Sprintf("%v", pluginData["name"]))
		}
		if len(plugins) > 0 {
			fmt.Printf("plugin(s) %v updated\n", plugins)
		}
	},
}

func verifyAccessModeStatus(mode string) string {
	if slices.Contains(connAccessModesFlag, mode) {
		return "enabled"
	}
	return "disabled"
}

func verifySchemaStatus(schema string, connType string) string {
	if schema == "" && connType == "database" {
		return "enabled"
	} else if schema == "" {
		return "disabled"
	} else if schema == "enabled" || schema == "disabled" {
		return schema
	}

	styles.PrintErrorAndExit("invalid value for schema status: %q, accepted values are: [enabled disabled]", schema)
	return ""
}

func parseEnvPerType() (envVar map[string]string, err error) {
	envVar = map[string]string{}
	var invalidEnvs []string
	for _, envvarStr := range connSecretFlag {
		key, val, found := strings.Cut(envvarStr, "=")
		if !found {
			invalidEnvs = append(invalidEnvs, envvarStr)
			continue
		}
		envType, keyenv, found := strings.Cut(key, ":")
		if found {
			key = keyenv
		} else {
			envType = "envvar"
		}
		if envType != "envvar" && envType != "filesystem" {
			return nil, fmt.Errorf("wrong environment type, acecpt one off: (envvar, filesystem)")
		}
		val, err = getEnvValue(val)
		if err != nil {
			return nil, fmt.Errorf("unable to get value: %v", err)
		}
		key = fmt.Sprintf("%v:%v", envType, key)
		envVar[key] = base64.StdEncoding.EncodeToString([]byte(val))
	}
	if len(invalidEnvs) > 0 {
		return nil, fmt.Errorf("invalid env vars, expected env=var. found=%v", invalidEnvs)
	}
	return envVar, nil
}

func getConnection(conf *clientconfig.Config, connectionName string) (bool, error) {
	resp, _, err := httpRequest(&apiResource{
		suffixEndpoint: fmt.Sprintf("/api/connections/%v", connectionName),
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

func getAgentIDByName(conf *clientconfig.Config, name string) (string, error) {
	data, _, err := httpRequest(&apiResource{suffixEndpoint: "/api/agents", conf: conf, decodeTo: "list"})
	if err != nil {
		return "", err
	}
	contents, ok := data.([]map[string]any)
	if !ok {
		return "", fmt.Errorf("failed type casting to []map[string]any, found=%T", data)
	}
	for _, m := range contents {
		if name == fmt.Sprintf("%v", m["name"]) {
			return fmt.Sprintf("%v", m["id"]), nil
		}
	}
	return "", nil
}

func validateNativeDbEnvs(e map[string]string) error {
	if e["envvar:HOST"] == "" || e["envvar:USER"] == "" || e["envvar:PASS"] == "" {
		return fmt.Errorf("missing required envs [HOST, USER, PASS] for %v type", connTypeFlag)
	}
	return nil
}

func validateSSHEnvs(e map[string]string) error {
	if e["envvar:HOST"] == "" || e["envvar:USER"] == "" || (e["envvar:PASS"] == "" && e["envvar:AUTHORIZED_SERVER_KEYS"] == "") {
		return fmt.Errorf("missing required envs [HOST, USER, PASS or AUTHORIZED_SERVER_KEYS] for %v type", connTypeFlag)
	}
	return nil
}

func validateTcpEnvs(e map[string]string) error {
	if e["envvar:HOST"] == "" || e["envvar:PORT"] == "" {
		return fmt.Errorf("missing required envs [HOST, PORT] for %v type", connTypeFlag)
	}
	return nil
}

func parseConnectionPlugins(conf *clientconfig.Config, connectionName, connectionID string) ([]map[string]any, error) {
	pluginList := []map[string]any{}
	for _, pluginOption := range connPuginFlag {
		pluginName, configStr, found := strings.Cut(pluginOption, ":")
		if !found && pluginName == "" {
			return nil, fmt.Errorf(`wrong format for plugin %q, expected "<plugin>:<config01>;config02>;..."`,
				pluginOption)
		}
		pluginName = strings.TrimSpace(pluginName)
		var connConfig []string
		for _, c := range strings.Split(configStr, ";") {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			connConfig = append(connConfig, c)
		}
		pl, err := getPlugin(conf, pluginName)
		if err != nil {
			return nil, err
		}
		if pl == nil {
			return nil, fmt.Errorf("plugin %s not found", pluginName)
		}
		pluginConnections, ok := pl["connections"].([]any)
		if !ok {
			return nil, fmt.Errorf("connections attribute with wrong structure [%s/%T]", pluginName, pl["connections"])
		}
		hasConnection := false
		for _, connObj := range pluginConnections {
			conn, ok := connObj.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("plugin connections with wrong structure [%s/%T]", pluginName, connObj)
			}
			if conn["name"] == connectionName {
				conn["config"] = connConfig
				hasConnection = true
				break
			}
		}
		if !hasConnection {
			pluginConnections = append(pluginConnections, map[string]any{
				"id":     connectionID,
				"name":   connectionName,
				"config": connConfig,
			})
		}
		pl["connections"] = pluginConnections
		pluginList = append(pluginList, pl)
	}
	return pluginList, nil
}

const (
	base64UriType string = "base64://"
	fileUriType   string = "file://"
)

// getEnvValue loads a raw inline value, a base64 inline value or a value from a file
//
// base64://<base64-enc-val> - decodes the base64 value using base64.StdEncoding
//
// file://<path/to/file> - loads based on the relative or absolute path
//
// If none of the above prefixes are found it returns the value as it is
func getEnvValue(val string) (string, error) {
	switch {
	case strings.HasPrefix(val, base64UriType):
		data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(val, base64UriType))
		if err != nil {
			return "", err
		}
		return string(data), nil
	case strings.HasPrefix(val, fileUriType):
		filePath := strings.TrimPrefix(val, fileUriType)
		isAbs := strings.HasPrefix(filePath, "/")
		if !isAbs {
			pwdDir, err := os.Getwd()
			if err != nil {
				return "", err
			}
			filePath = filepath.Join(pwdDir, filePath)
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	return val, nil
}
