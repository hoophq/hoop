package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/runopsio/hoop/client/cmd/styles"
	clientconfig "github.com/runopsio/hoop/client/config"
	"github.com/spf13/cobra"
)

var targetToConnection = &cobra.Command{
	Use:    "target-to-connection NAME",
	Short:  "Generates command to migrate from runops targets to hoop connections.",
	Hidden: true,
	PreRun: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Usage()
			styles.PrintErrorAndExit("missing resource name")
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		config := clientconfig.GetClientConfigOrDie()
		_ = config
		connectionName := args[0]
		tgt := getRunopsTargetOrDie(connectionName)
		// agentName := fmt.Sprintf("%v", target["tags"])
		// if agentName == "" {
		// 	agentName = "main"
		// }
		// enableDLP := target["redact"] == "all"
		// targetType := fmt.Sprintf("%v", target["type"])
		// secretProvider := fmt.Sprintf("%v", target["secret_provider"])
		// secretPath := fmt.Sprintf("%v", target["secret_path"])

		// secretRunopsConfig := fmt.Sprintf("%v", target["config"])

		agentID, err := getAgentIDByName(config, tgt.agentName())
		if err != nil {
			styles.PrintErrorAndExit("failed to fetch agent %q for target, reason=%v", tgt.agentName(), err)
		}
		if agentID == "" {
			fmt.Printf("agent %q not found for target, create a new one:\n", tgt.agentName())
			fmt.Println("hoop admin create agent main")
			os.Exit(0)
		}
		cmdList := []string{}
		envVarMap := map[string]string{}
		switch tgt.SecretProvider {
		case "env-var":
			if tgt.SecretPath == "" {
				styles.PrintErrorAndExit(`secret provider %q has empty "secret_path" attribute`, tgt.SecretProvider)
			}
			cmdList, envVarMap, err = parseEnvVarByType(tgt)
			if err != nil {
				styles.PrintErrorAndExit(err.Error())
			}

		case "aws":
			// TODO
		case "runops":
			if tgt.Config == "" {
				styles.PrintErrorAndExit(`secret provider "runops" has empty "config" attribute`)
			}

		default:
			styles.PrintErrorAndExit("secret provider %q not supported", tgt.SecretProvider)
		}

		// _, _ = enableDLP, agentName
		// secretMappingJson := map[string]any{}
		// if target["secret_mapping"] != "" {
		// 	sm := fmt.Sprintf("%v", target["secret_mapping"])
		// 	if err := json.Unmarshal([]byte(sm), &secretMappingJson); err != nil {
		// 		styles.PrintErrorAndExit("failed decoding secret mapping from target, reason=%v", err)
		// 	}
		// }

		connectionBody := map[string]any{
			"name":     connectionName,
			"type":     "command-line",
			"command":  cmdList,
			"secret":   envVarMap,
			"agent_id": agentID,
		}

		apir := &apiResource{suffixEndpoint: "/api/connections", conf: config, decodeTo: "raw"}
		rawResponse, err := httpBodyRequest(apir, "POST", connectionBody)
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
		}
		if apir.decodeTo == "raw" {
			jsonData, _ := rawResponse.([]byte)
			fmt.Println(string(jsonData))
			return
		}
		fmt.Printf("connection %q created\n", connectionName)

	},
}

// Problems:
// secrets are expanded by mapping only
//   - change to expand all secret mapped
//
// custom commands
// groups
// redact
func parseEnvVarByType(t *RunopsTarget) ([]string, map[string]string, error) {
	envVar := map[string]string{}
	cmdList := []string{}
	// dock: postgres, mysql, sql-server, python, bash, k8s
	// ebanx: postgres, mysql, bash, python, k8s
	// rdstation: postgres-csv, postgres, mongodb, mysql
	// Magnetis: postgres, k8s-exec, mysql, python, rails-console
	// enjoei: rails-console-ecs, mysql

	secretKeyFn := func(key string) string {
		switch t.SecretProvider {
		case "aws":
			return fmt.Sprintf("aws:%s:%s", t.SecretPath, t.secretKey(key))
		case "envvar":
			return fmt.Sprintf("envjson:%s:%s", t.SecretPath, t.secretKey(key))
		case "runops":
			secretVal := t.secretKey(key)
			if secretVal == "" {
				styles.PrintErrorAndExit("provider=runops - secret value is empty for %q", key)
			}
			return secretVal
		}
		styles.PrintErrorAndExit("secret provider %q not implemented", t.SecretProvider)
		return ""
	}

	switch t.Type {
	case "mysql", "mysql-csv":
		// TODO: user mysql delimiter
		envVar["HOST"] = secretKeyFn("MYSQL_HOST")
		envVar["USER"] = secretKeyFn("MYSQL_USER")
		envVar["MYSQL_PWD"] = secretKeyFn("MYSQL_PASS")
		envVar["DB"] = secretKeyFn("MYSQL_DB")
		envVar["PORT"] = "3306"
		cmdList = []string{
			"mysql",
			"--port=$PORT",
			"-h$HOST",
			"-u$USER",
			"-D$DB",
			"--comments"}
	case "postgres", "postgres-csv":
		// TODO: user psql delimiter option
		// TODO: add ssl options
		envVar["HOST"] = secretKeyFn("PG_HOST")
		envVar["USER"] = secretKeyFn("PG_USER")
		envVar["PGPASSWORD"] = secretKeyFn("PG_PASS")
		envVar["DB"] = secretKeyFn("PG_DB")
		envVar["PORT"] = "5432"
		cmdList = []string{
			"psql", "-A", "-F\t",
			"--port=$PORT",
			"-h$HOST",
			"-U$USER",
			"-d$DB",
			"-vON_ERROR_STOP=1"}
	case "sql-server":
		envVar["MSSQL_CONNECTION_URI"] = secretKeyFn("PG_HOST")
		envVar["MSSQL_USER"] = secretKeyFn("PG_HOST")
		envVar["MSSQL_PASS"] = secretKeyFn("PG_HOST")
		envVar["MSSQL_DB"] = secretKeyFn("PG_HOST")
		// TODO: test it passing to stdin
		cmdList = []string{
			"sqlcmd", "-b", "-r",
			"-S", "$MSSQL_CONNECTION_URI",
			"-U", "$MSSQL_USER",
			"-P", "$MSSQL_PASS",
			"-d", "$MSSQL_DB"}
	case "python":
	case "mongodb":
	// all env-var types
	case "k8s", "k8s-exec":
	// used mostly by enjoei
	case "rails-console-ecs":
	// used by transfeera and dock
	case "node":
	case "bash": // TODO

	default:
		// elixir, ecs-exec, heroku, k8s-apply, rails-console-k8s, rails-console
		return nil, nil, fmt.Errorf("target %q not supported", t.Type)
	}
	return cmdList, envVar, nil
}

type RunopsTarget struct {
	Name           string `json:"name"`
	Status         string `json:"status"`
	Type           string `json:"type"`
	Tags           string `json:"tags"`
	SecretProvider string `json:"secret_provider"`
	SecretPath     string `json:"secret_path"`
	Config         string `json:"config"`
	SecretMapping  string `json:"secret_mapping"`

	// TODO
	Redact        string   `json:"redact"`
	Groups        []string `json:"groups"`
	CustomCommand string   `json:"custom_command"`

	secretMapping map[string]string
	config        map[string]string
}

func (t *RunopsTarget) enableDLP() bool { return t.Redact == "all" }
func (t *RunopsTarget) agentName() string {
	if t.Tags == "" {
		return "main"
	}
	return t.Tags
}

// secretKey do a best effort to fetch the secret from the mapping, if it doesn't
// exits returns the same key
func (t *RunopsTarget) secretKey(key string) string {
	if t.SecretProvider == "runops" {
		return t.config[key]
	}
	secretKeyVal, ok := t.secretMapping[key]
	if !ok {
		secretKeyVal = key
	}
	return secretKeyVal
}

func getRunopsTargetOrDie(connectionName string) *RunopsTarget {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		styles.PrintErrorAndExit(err.Error())
	}
	runopsConfig := filepath.Join(homeDir, ".runops", "config")
	tokenBytes, err := os.ReadFile(runopsConfig)
	if err != nil {
		styles.PrintErrorAndExit("failed reading runops config file, try to login at runops first. reason=%v\n", err)
	}
	tokenBytes = bytes.ReplaceAll(tokenBytes, []byte(`Bearer `), []byte(``))
	req, _ := http.NewRequest("GET", "https://api.runops.io/v1/targets/"+connectionName, nil)
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %v", tokenBytes))
	req.Header.Add("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		styles.PrintErrorAndExit("failed fetching target, reason=%v", err)
	}
	defer resp.Body.Close()
	var target RunopsTarget
	if resp.StatusCode != 200 {
		data, _ := io.ReadAll(resp.Body)
		styles.PrintErrorAndExit("failed fetching target, code=%v, response=%v",
			resp.Status, string(data))
	}
	if err := json.NewDecoder(resp.Body).Decode(&target); err != nil {
		styles.PrintErrorAndExit("failed decoding target, reason=%v", err)
	}
	if target.Status != "active" {
		styles.PrintErrorAndExit("target is not active, status=%v", target.Status)
	}
	target.secretMapping = map[string]string{}
	target.config = map[string]string{}
	if target.SecretMapping != "" {
		buf := bytes.NewBufferString(target.SecretMapping)
		if err := json.NewDecoder(buf).Decode(&target.secretMapping); err != nil {
			styles.PrintErrorAndExit("failed decoding secret mapping %v, reason=%v",
				target.secretMapping, err)
		}
	}
	if target.Config != "" && target.SecretProvider == "runops" {
		buf := bytes.NewBufferString(target.Config)
		if err := json.NewDecoder(buf).Decode(&target.config); err != nil {
			styles.PrintErrorAndExit("failed decoding secret mapping %v, reason=%v",
				target.secretMapping, err)
		}
	}
	return &target
}
