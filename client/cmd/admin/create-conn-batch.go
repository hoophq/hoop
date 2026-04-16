package admin

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// BatchConnectionFile represents the YAML file format for batch connection creation.
//
// Example file:
//
//	agent: default
//	type: database/postgres
//	overwrite: false
//	tags:
//	  env: production
//	  team: platform
//	access_modes:
//	  - connect
//	  - exec
//	  - runbooks
//	connections:
//	  - name: users-db
//	    env:
//	      HOST: 10.0.1.10
//	      USER: admin
//	      PASS: file:///secrets/users-db.txt
//	      PORT: "5432"
//	  - name: orders-db
//	    env:
//	      HOST: 10.0.1.11
//	      USER: admin
//	      PASS: base64://c2VjcmV0
//	      PORT: "5432"
type BatchConnectionFile struct {
	Agent       string            `yaml:"agent"`
	Type        string            `yaml:"type"`
	Overwrite   bool              `yaml:"overwrite"`
	Tags        map[string]string `yaml:"tags"`
	AccessModes []string          `yaml:"access_modes"`
	Schema      string            `yaml:"schema"`
	Connections []BatchConnection `yaml:"connections"`
}

// BatchConnection represents a single connection entry in the batch file.
// Each entry can override the top-level agent, type, tags, and access_modes.
type BatchConnection struct {
	Name        string            `yaml:"name"`
	Agent       string            `yaml:"agent"`
	Type        string            `yaml:"type"`
	Env         map[string]string `yaml:"env"`
	Command     []string          `yaml:"command"`
	Tags        map[string]string `yaml:"tags"`
	AccessModes []string          `yaml:"access_modes"`
	Schema      string            `yaml:"schema"`
	Overwrite   *bool             `yaml:"overwrite"`
}

var fromFileFlag string

func init() {
	createConnectionBatchCmd.Flags().StringVar(&fromFileFlag, "from-file", "", "Path to a YAML file defining multiple connections")
	createConnectionBatchCmd.MarkFlagRequired("from-file")
}

var createConnectionBatchExamplesDesc = `
  # Create multiple postgres connections from a YAML file
  hoop admin create connections --from-file connections.yaml

  # Example YAML file (connections.yaml):
  #
  # agent: default
  # type: database/postgres
  # connections:
  #   - name: users-db
  #     env:
  #       HOST: 10.0.1.10
  #       USER: admin
  #       PASS: secret
  #       PORT: "5432"
  #   - name: orders-db
  #     env:
  #       HOST: 10.0.1.11
  #       USER: admin
  #       PASS: file:///secrets/orders-db.txt
  #       PORT: "5432"
`

var createConnectionBatchCmd = &cobra.Command{
	Use:     "connections --from-file FILE",
	Aliases: []string{"conns"},
	Example: createConnectionBatchExamplesDesc,
	Short:   "Create multiple connection resources from a YAML file.",
	Run: func(cmd *cobra.Command, args []string) {
		config := clientconfig.GetClientConfigOrDie()
		batchFile, err := parseBatchFile(fromFileFlag)
		if err != nil {
			styles.PrintErrorAndExit("failed to parse file %q: %v", fromFileFlag, err)
		}

		if len(batchFile.Connections) == 0 {
			styles.PrintErrorAndExit("no connections defined in %q", fromFileFlag)
		}

		fmt.Printf("creating %d connection(s) from %s\n\n", len(batchFile.Connections), fromFileFlag)

		var succeeded, failed int
		var errors []string

		for i, conn := range batchFile.Connections {
			if conn.Name == "" {
				errMsg := fmt.Sprintf("[%d/%d] skipped: missing connection name", i+1, len(batchFile.Connections))
				fmt.Println(styles.ClientErrorSimple(errMsg))
				errors = append(errors, errMsg)
				failed++
				continue
			}

			connName := NormalizeResourceName(conn.Name)
			result := createSingleConnection(config, batchFile, conn, connName, i+1, len(batchFile.Connections))
			if result != nil {
				errMsg := fmt.Sprintf("[%d/%d] %s: %v", i+1, len(batchFile.Connections), connName, result)
				fmt.Println(styles.ClientErrorSimple(errMsg))
				errors = append(errors, errMsg)
				failed++
			} else {
				succeeded++
			}
		}

		// Print summary
		fmt.Printf("\ndone: %d succeeded, %d failed out of %d total\n", succeeded, failed, len(batchFile.Connections))
		if len(errors) > 0 {
			fmt.Println("\nfailed connections:")
			for _, e := range errors {
				fmt.Printf("  - %s\n", e)
			}
			os.Exit(1)
		}
	},
}

func createSingleConnection(config *clientconfig.Config, batch *BatchConnectionFile, conn BatchConnection, connName string, index, total int) error {
	// Resolve effective values (connection-level overrides top-level)
	agentName := batch.Agent
	if conn.Agent != "" {
		agentName = conn.Agent
	}
	if agentName == "" {
		return fmt.Errorf("no agent specified (set at top-level or per-connection)")
	}

	connTypeStr := batch.Type
	if conn.Type != "" {
		connTypeStr = conn.Type
	}
	if connTypeStr == "" {
		return fmt.Errorf("no connection type specified (set at top-level or per-connection)")
	}

	overwrite := batch.Overwrite
	if conn.Overwrite != nil {
		overwrite = *conn.Overwrite
	}

	accessModes := batch.AccessModes
	if len(conn.AccessModes) > 0 {
		accessModes = conn.AccessModes
	}
	if len(accessModes) == 0 {
		accessModes = defaultAccessModes
	}

	schema := batch.Schema
	if conn.Schema != "" {
		schema = conn.Schema
	}

	tags := map[string]string{}
	for k, v := range batch.Tags {
		tags[k] = v
	}
	for k, v := range conn.Tags {
		tags[k] = v
	}

	// Determine method
	method := "POST"
	actionName := "created"
	if overwrite {
		exists, err := getConnection(config, connName)
		if err != nil {
			return fmt.Errorf("failed checking existence: %v", err)
		}
		if exists {
			method = "PUT"
			actionName = "updated"
		}
	}

	// Parse environment variables
	envVar, err := parseBatchEnvVars(conn.Env)
	if err != nil {
		return fmt.Errorf("invalid env vars: %v", err)
	}

	// Parse and validate connection type
	connType, subType, _ := strings.Cut(connTypeStr, "/")
	protocolConnectionType := pb.ToConnectionType(connType, subType)

	if !skipStrictValidation {
		if err := validateConnectionType(protocolConnectionType, envVar, conn.Command); err != nil {
			return err
		}
	}

	// Resolve agent ID
	agentID, err := getAgentIDByName(config, agentName)
	if err != nil {
		return fmt.Errorf("failed resolving agent %q: %v", agentName, err)
	}
	if agentID == "" && !skipStrictValidation {
		return fmt.Errorf("agent %q not found", agentName)
	}

	// Build API resource
	apir := &apiResource{
		resourceType:   "connection",
		name:           connName,
		method:         method,
		conf:           config,
		resourceCreate: true,
		resourceUpdate: true,
		decodeTo:       "object",
	}
	if method == "POST" {
		apir.suffixEndpoint = "/api/connections"
	} else {
		apir.suffixEndpoint = fmt.Sprintf("/api/connections/%s", connName)
	}
	if outputFlag == "json" {
		apir.decodeTo = "raw"
	}

	connectionBody := map[string]any{
		"name":                 connName,
		"type":                 connType,
		"subtype":              subType,
		"command":              conn.Command,
		"secret":               envVar,
		"agent_id":             agentID,
		"redact_enabled":       true,
		"connection_tags":      tags,
		"access_mode_runbooks": batchAccessModeStatus(accessModes, "runbooks"),
		"access_mode_exec":     batchAccessModeStatus(accessModes, "exec"),
		"access_mode_connect":  batchAccessModeStatus(accessModes, "connect"),
		"access_schema":        verifySchemaStatus(schema, connType),
	}

	log.Debugf("creating connection %v (%d/%d)", connName, index, total)
	_, err = httpBodyRequest(apir, method, connectionBody)
	if err != nil {
		return err
	}

	fmt.Printf("[%d/%d] connection %s %s\n", index, total, connName, actionName)
	return nil
}

func parseBatchFile(filePath string) (*BatchConnectionFile, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %v", err)
	}

	var batch BatchConnectionFile
	if err := yaml.Unmarshal(data, &batch); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %v", err)
	}

	return &batch, nil
}

func parseBatchEnvVars(envMap map[string]string) (map[string]string, error) {
	result := map[string]string{}
	for key, val := range envMap {
		// Support the same value formats as the single create command:
		// raw values, base64://<content>, file:///path
		resolvedVal, err := getEnvValue(val)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve value for %q: %v", key, err)
		}
		envKey := fmt.Sprintf("envvar:%s", key)
		result[envKey] = base64.StdEncoding.EncodeToString([]byte(resolvedVal))
	}
	return result, nil
}

func validateConnectionType(protocolType pb.ConnectionType, envVar map[string]string, cmdList []string) error {
	switch protocolType {
	case pb.ConnectionTypeCommandLine:
		if len(cmdList) == 0 {
			return fmt.Errorf("command-line type must have at least one command")
		}
	case pb.ConnectionTypeTCP:
		return validateTcpEnvs(envVar)
	case pb.ConnectionTypePostgres, pb.ConnectionTypeMySQL, pb.ConnectionTypeMSSQL:
		return validateNativeDbEnvs(envVar)
	case pb.ConnectionTypeMongoDB:
		if envVar["envvar:CONNECTION_STRING"] == "" {
			return fmt.Errorf("missing required CONNECTION_STRING env for %v", pb.ConnectionTypeMongoDB)
		}
	case pb.ConnectionTypeHttpProxy:
		if envVar["envvar:REMOTE_URL"] == "" {
			return fmt.Errorf("missing required REMOTE_URL env for %v", pb.ConnectionTypeHttpProxy)
		}
	case pb.ConnectionTypeKubernetes:
		// no validation needed
	case pb.ConnectionTypeSSH:
		return validateSSHEnvs(envVar)
	default:
		return fmt.Errorf("invalid connection type %q", protocolType)
	}
	return nil
}

func batchAccessModeStatus(modes []string, mode string) string {
	for _, m := range modes {
		if m == mode {
			return "enabled"
		}
	}
	return "disabled"
}
