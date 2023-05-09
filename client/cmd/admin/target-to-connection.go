package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/runopsio/hoop/client/cmd/styles"
	clientconfig "github.com/runopsio/hoop/client/config"
	"github.com/spf13/cobra"
)

var (
	targetToConnGrpcURL string
	createConnTmpl      = `hoop admin create connection {{ .name }} --agent {{ .agent_id }} \
	--overwrite \
	--type {{ .type }} \
	{{- range $key, $val := .plugins }}
	--plugin '{{ $key }}{{ $val }}' \
	{{- end }}
	{{- range $key, $val := .secret }}
	--env {{ $key }}={{ $val }} \
	{{- end }}
	-- {{ .command }}
`
	helmInstallAgentTmpl = `VERSION=$(curl -s https://hoopartifacts.s3.amazonaws.com/release/latest.txt)
helm upgrade --install hoopagent https://hoopartifacts.s3.amazonaws.com/release/$VERSION/hoopagent-chart-$VERSION.tgz \
	--set "config.gateway.grpc_url={{ .grpc_url }}" \
	--set "config.gateway.token={{ .token }}"
`
)

func init() {
	targetToConnection.Flags().StringVar(&targetToConnGrpcURL, "grpc-url", "", "The grpc address of hoop gateway instance")
	targetToConnection.MarkFlagRequired("grpc-url")
}

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
		connectionName := args[0]
		tgt := getRunopsTargetOrDie(connectionName)

		agentID, err := getAgentIDByName(config, tgt.agentName())
		if err != nil {
			styles.PrintErrorAndExit("failed to fetch agent %q for target, reason=%v", tgt.agentName(), err)
		}

		cmdList, envVarMap, err := parseEnvVarByType(tgt)
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
		}

		connectionBody := map[string]any{
			"name":     connectionName,
			"type":     "command-line",
			"command":  cmdList,
			"secret":   envVarMap,
			"agent_id": agentID,
		}

		plugins := map[string]string{"audit": ""}
		if tgt.getSlackChannel() != "" {
			pl, err := getPlugin(config, "slack")
			if err != nil {
				styles.PrintErrorAndExit("failed obtaining slack plugn, err=%v", err)
			}
			if pl == nil {
				fmt.Println("this targets requires the plugin slack, configure it before proceeding")
				fmt.Println("https://hoop.dev/docs/plugins/slack")
				os.Exit(1)
			}
			plugins["slack"] = ""
		}
		if agentID == "" {
			fmt.Println("# agent creation/installation")
			fmt.Printf("AGENT_TOKEN=$(hoop admin create agent %s)\n", tgt.agentName())
			connectionBody["agent_id"] = tgt.agentName()
			fmt.Println(execGoTemplate(helmInstallAgentTmpl, map[string]string{
				"token":    "$AGENT_TOKEN",
				"grpc_url": targetToConnGrpcURL,
			}))
		}
		if len(tgt.Groups) > 0 {
			pl, err := getPlugin(config, "access_control")
			if err != nil {
				styles.PrintErrorAndExit("failed obtaining access_control plugin, err=%v", err)
			}
			if pl == nil {
				fmt.Println("# enable access control plugin because the target has groups")
				fmt.Println("hoop admin create plugin access_control\n")
			}
			plugins["access_control:"] = strings.Join(tgt.Groups, ";")
		}
		if tgt.enableReview() && tgt.ReviewGroups != "" {
			pl, err := getPlugin(config, "review")
			if err != nil {
				styles.PrintErrorAndExit("failed obtaining review plugin, err=%v", err)
			}
			if pl == nil {
				fmt.Println("# enable review plugin because the target has review groups")
				fmt.Println("hoop admin create plugin review\n")
			}
			plugins["review:"] = strings.ReplaceAll(tgt.ReviewGroups, ",", ";")
		}

		if tgt.SecretProvider == "env-var" || tgt.SecretProvider == "aws" {
			pl, err := getPlugin(config, "secretsmanager")
			if err != nil {
				styles.PrintErrorAndExit("failed obtaining secretsmanager plugin, err=%v", err)
			}
			if pl == nil {
				fmt.Println("# enabling because the target secret provider is aws or env-var")
				fmt.Println("hoop admin create plugin secretsmanager --source hoop/secretsmanager\n")
			}
			plugins["secretsmanager"] = ""
		}

		if tgt.enableDLP() {
			plugins["dlp"] = ""
		}

		connectionBody["plugins"] = plugins
		connectionBody["command"] = strings.Join(cmdList, " ")
		fmt.Println("# the connection")
		fmt.Println(execGoTemplate(createConnTmpl, connectionBody))
	},
}

func parseEnvVarByType(t *RunopsTarget) ([]string, map[string]string, error) {
	if t.CustomCommand != "" {
		return nil, nil, fmt.Errorf("target has custom command, not implemented yet")
	}
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
		case "env-var":
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
		envVar["HOST"] = secretKeyFn("MYSQL_HOST")
		envVar["USER"] = secretKeyFn("MYSQL_USER")
		envVar["MYSQL_PWD"] = secretKeyFn("MYSQL_PASS")
		envVar["DB"] = secretKeyFn("MYSQL_DB")
		envVar["PORT"] = "3306"
		cmdList = []string{
			"mysql",
			"--port", "'$PORT'",
			"-h", "'$HOST'",
			"-u", "'$USER'",
			"-D", "'$DB'",
			"--comments"}
	case "postgres", "postgres-csv":
		// TODO: add ssl options
		envVar["HOST"] = secretKeyFn("PG_HOST")
		envVar["USER"] = secretKeyFn("PG_USER")
		envVar["PGPASSWORD"] = secretKeyFn("PG_PASS")
		envVar["DB"] = secretKeyFn("PG_DB")
		envVar["PORT"] = "5432"
		cmdList = []string{
			"psql",
			"--port", "'$PORT'",
			"-h", "'$HOST'",
			"-U", "'$USER'",
			"-d", "'$DB'",
			"-vON_ERROR_STOP=1"}
	case "sql-server":
		envVar["MSSQL_CONNECTION_URI"] = secretKeyFn("PG_HOST")
		envVar["MSSQL_USER"] = secretKeyFn("PG_HOST")
		envVar["MSSQL_PASS"] = secretKeyFn("PG_HOST")
		envVar["MSSQL_DB"] = secretKeyFn("PG_HOST")
		// TODO: test it passing to stdin
		cmdList = []string{
			"sqlcmd", "-b", "-r",
			"-S", "'$MSSQL_CONNECTION_URI'",
			"-U", "'$MSSQL_USER'",
			"-P", "'$MSSQL_PASS'",
			"-d", "'$MSSQL_DB'"}
	// case "python":
	// case "mongodb":
	// // all env-var types
	// case "k8s", "k8s-exec":
	// // used mostly by enjoei
	// case "rails-console-ecs":
	// // used by transfeera and dock
	// case "node":
	// case "bash":

	default:
		// elixir, ecs-exec, heroku, k8s-apply, rails-console-k8s, rails-console
		return nil, nil, fmt.Errorf("target %q not supported", t.Type)
	}
	return cmdList, envVar, nil
}

type RunopsTarget struct {
	Name           string   `json:"name"`
	Status         string   `json:"status"`
	Type           string   `json:"type"`
	Tags           string   `json:"tags"`
	SecretProvider string   `json:"secret_provider"`
	SecretPath     string   `json:"secret_path"`
	Config         string   `json:"config"`
	SecretMapping  string   `json:"secret_mapping"`
	Redact         string   `json:"redact"`
	ReviewType     string   `json:"review_type"`
	ReviewGroups   string   `json:"reviewers"`
	SlackChannel   *string  `json:"channel_name"`
	Groups         []string `json:"groups"`

	// TODO
	CustomCommand string `json:"custom_command"`

	secretMapping map[string]string
	config        map[string]string
}

func (t *RunopsTarget) getSlackChannel() string {
	if t.SlackChannel != nil {
		return *t.SlackChannel
	}
	return ""
}
func (t *RunopsTarget) enableReview() bool { return t.ReviewType != "none" }
func (t *RunopsTarget) enableDLP() bool    { return t.Redact == "all" }
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
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %v", string(tokenBytes)))
	req.Header.Add("Accept", "application/json")
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

func execGoTemplate(tmpl string, data any) string {
	t, err := template.New("").Parse(tmpl)
	if err != nil {
		styles.PrintErrorAndExit("failed parsing template, err=%v", err)
	}
	buf := bytes.NewBufferString("")
	if err := t.Execute(buf, data); err != nil {
		styles.PrintErrorAndExit("failed executing template: %v", err)
	}
	return buf.String()
}
