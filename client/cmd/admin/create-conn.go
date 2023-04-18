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
	connAgentFlag     string
	connTypeFlag      string
	connSecretFlag    []string
	connOverwriteFlag bool
)

func init() {
	createConnectionCmd.Flags().StringVarP(&connAgentFlag, "agent", "a", "", "Name of the agent")
	createConnectionCmd.Flags().StringVarP(&connTypeFlag, "type", "t", "command-line", "Type of the connection. One off: (command-line,postgres,mysql,tcp)")
	createConnectionCmd.Flags().BoolVar(&connOverwriteFlag, "overwrite", false, "It will create or update it if a connection already exists")
	createConnectionCmd.Flags().StringSliceVarP(&connSecretFlag, "env", "e", nil, "The environment variables of the connection")
	createConnectionCmd.MarkFlagRequired("agent")

}

var createConnExamplesDesc = `
hoop admin create connection hello-hoop -a test-agent -- bash -c 'echo hello hoop'
hoop admin create connection tcpsvc -a test-agent -t tcp -e HOST=127.0.0.1 -e PORT=3000
`
var createConnectionCmd = &cobra.Command{
	Use:     "connection NAME [-- COMMAND]",
	Aliases: []string{"conn"},
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
		if connOverwriteFlag {
			exists, err := getConnection(clientconfig.GetClientConfigOrDie(), args[0])
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
			cmdList = append(cmdList, args[1:]...)
		}
		envVar, err := parseEnvPerType()
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
		}
		switch connTypeFlag {
		case "command-line":
			if len(cmdList) == 0 {
				styles.PrintErrorAndExit("command-line type must be at least one command")
			}
		case "tcp":
			if err := validateTcpEnvs(envVar); err != nil {
				styles.PrintErrorAndExit(err.Error())
			}
		case "postgres", "mysql":
			if err := validateNativeDbEnvs(envVar); err != nil {
				styles.PrintErrorAndExit(err.Error())
			}
		default:
			styles.PrintErrorAndExit(err.Error())
		}
		connectionBody := map[string]any{
			"name":     apir.name,
			"type":     connTypeFlag,
			"command":  cmdList,
			"secret":   envVar,
			"agent_id": connAgentFlag,
		}

		resp, err := httpBodyRequest(apir, method, connectionBody)
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
		}
		if apir.decodeTo == "raw" {
			jsonData, _ := resp.([]byte)
			fmt.Println(string(jsonData))
			return
		}
		fmt.Printf("connection %v %v\n", apir.name, actionName)
	},
}

func parseEnvPerType() (map[string]string, error) {
	envVar := map[string]string{}
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
		if envType != "envvar" && envType != "filesystem" &&
			envType != "b64-envvar" && envType != "b64-filesystem" {
			return nil, fmt.Errorf("wrong environment type, acecpt one off: ([b64-]envvar, [b64-]filesystem)")
		}
		isBase64Env := strings.HasPrefix(envType, "b64-")
		envType = strings.TrimPrefix(envType, "b64-")
		key = fmt.Sprintf("%v:%v", envType, strings.ToUpper(key))
		envVar[key] = val
		if !isBase64Env {
			envVar[key] = base64.StdEncoding.EncodeToString([]byte(val))
		}
	}
	if len(invalidEnvs) > 0 {
		return nil, fmt.Errorf("invalid env vars, expected env=var. found=%v", invalidEnvs)
	}
	return envVar, nil
}

func getConnection(conf *clientconfig.Config, connectionName string) (bool, error) {
	resp, err := httpRequest(&apiResource{
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

func validateNativeDbEnvs(e map[string]string) error {
	if e["envvar:HOST"] == "" || e["envvar:USER"] == "" || e["envvar:PASS"] == "" {
		return fmt.Errorf("missing required envs [HOST,USER,PASS] for %v type", connTypeFlag)
	}
	return nil
}

func validateTcpEnvs(e map[string]string) error {
	if e["envvar:HOST"] == "" || e["envvar:PORT"] == "" {
		return fmt.Errorf("missing required envs [HOST,PORT] for %v type", connTypeFlag)
	}
	return nil
}
