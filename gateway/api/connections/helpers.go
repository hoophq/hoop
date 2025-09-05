package apiconnections

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
)

var (
	tagsValRe, _           = regexp.Compile(`^[a-zA-Z0-9_]+(?:[-\.]?[a-zA-Z0-9_]+){0,128}$`)
	connectionTagsKeyRe, _ = regexp.Compile(`^[a-zA-Z0-9_]+(?:[-\./]?[a-zA-Z0-9_]+){0,}$`)
	connectionTagsValRe, _ = regexp.Compile(`^[a-zA-Z0-9-_\+=@\/:\s]+$`)
)

func setConnectionDefaults(req *openapi.Connection) {
	if req.Secrets == nil {
		req.Secrets = map[string]any{}
	}

	hasMongoConnStr := req.Secrets["envvar:CONNECTION_STRING"] != ""
	defaultCommand, defaultEnvVars := GetConnectionDefaults(req.Type, req.SubType, hasMongoConnStr)

	if len(req.Command) == 0 {
		req.Command = defaultCommand
	}

	for key, val := range defaultEnvVars {
		if _, isset := req.Secrets[key]; !isset {
			req.Secrets[key] = val
		}
	}
}

func GetConnectionDefaults(connType, connSubType string, useMongoConnStr bool) (cmd []string, envs map[string]any) {
	envs = map[string]any{}
	switch pb.ToConnectionType(connType, connSubType) {
	case pb.ConnectionTypePostgres:
		cmd = []string{"psql", "-v", "ON_ERROR_STOP=1", "-A", "-F\t", "-P", "pager=off", "-h", "$HOST", "-U", "$USER", "--port=$PORT", "$DB"}
	case pb.ConnectionTypeMySQL:
		cmd = []string{"mysql", "-h$HOST", "-u$USER", "--port=$PORT", "-D$DB"}
	case pb.ConnectionTypeMSSQL:
		envs["envvar:INSECURE"] = base64.StdEncoding.EncodeToString([]byte(`false`))
		cmd = []string{
			"sqlcmd", "--exit-on-error", "--trim-spaces", "-s\t", "-r",
			"-S$HOST:$PORT", "-U$USER", "-d$DB", "-i/dev/stdin"}
	case pb.ConnectionTypeOracleDB:
		envs["envvar:LD_LIBRARY_PATH"] = base64.StdEncoding.EncodeToString([]byte(`/opt/oracle/instantclient_19_24`))
		cmd = []string{"sqlplus", "-s", "$USER/$PASS@$HOST:$PORT/$SID"}
	case pb.ConnectionTypeMongoDB:
		envs["envvar:OPTIONS"] = base64.StdEncoding.EncodeToString([]byte(`tls=true`))
		envs["envvar:PORT"] = base64.StdEncoding.EncodeToString([]byte(`27017`))
		cmd = []string{"mongo", "--quiet", "mongodb://$USER:$PASS@$HOST:$PORT/?$OPTIONS"}
		if useMongoConnStr {
			envs = nil
			cmd = []string{"mongo", "--quiet", "$CONNECTION_STRING"}
		}
	}
	return
}

func coerceToMapString(src map[string]any) map[string]string {
	dst := map[string]string{}
	for k, v := range src {
		dst[k] = fmt.Sprintf("%v", v)
	}
	return dst
}

func coerceToAnyMap(src map[string]string) map[string]any {
	dst := map[string]any{}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func validateConnectionRequest(req openapi.Connection) error {
	errors := []string{}
	if err := apivalidation.ValidateResourceName(req.Name); err != nil {
		errors = append(errors, err.Error())
	}
	// TODO: deprecated
	for _, val := range req.Tags {
		if !tagsValRe.MatchString(val) {
			errors = append(errors, "tags: values must contain between 1 and 128 alphanumeric characters, it may include (-), (_) or (.) characters")
		}
	}
	if len(errors) > 0 {
		return fmt.Errorf(strings.Join(errors, "; "))
	}

	if len(req.ConnectionTags) > 10 {
		return fmt.Errorf("max tag association reached (10)")
	}

	for key, val := range req.ConnectionTags {
		// if strings.HasPrefix(key, "hoop.dev/") {
		// 	errors = append(errors, "connection_tags: keys must not use the reserverd prefix hoop.dev/")
		// 	continue
		// }

		if (len(key) < 1 || len(key) > 64) || !connectionTagsKeyRe.MatchString(key) {
			errors = append(errors,
				fmt.Sprintf("connection_tags (%v), keys must contain between 1 and 64 alphanumeric characters, ", key)+
					"it may include (-), (_), (/), or (.) characters and it must not end with (-), (/) or (-)")
		}
		if (len(val) < 1 || len(val) > 256) || !connectionTagsValRe.MatchString(val) {
			errors = append(errors, fmt.Sprintf("connection_tags (%v), values must contain between 1 and 256 alphanumeric characters, ", key)+
				"it may include space, (-), (_), (/), (+), (@), (:), (=) or (.) characters")
		}
	}
	if len(errors) > 0 {
		return fmt.Errorf(strings.Join(errors, "; "))
	}
	return nil
}

var reSanitize, _ = regexp.Compile(`^[a-zA-Z0-9_]+(?:[-\.]?[a-zA-Z0-9_]+){1,128}$`)
var errInvalidOptionVal = errors.New("option values must contain between 1 and 127 alphanumeric characters, it may include (-), (_) or (.) characters")

func validateListOptions(urlValues url.Values) (o models.ConnectionFilterOption, err error) {
	if reSanitize == nil {
		return o, fmt.Errorf("failed compiling sanitize regex on listing connections")
	}
	for key, values := range urlValues {
		switch key {
		case "agent_id":
			o.AgentID = values[0]
		case "type":
			o.Type = values[0]
		case "subtype":
			o.SubType = values[0]
		case "managed_by":
			o.ManagedBy = values[0]
		case "tag_selector":
			o.TagSelector = values[0]
		case "tags":
			if len(values[0]) > 0 {
				for _, tagVal := range strings.Split(values[0], ",") {
					if !reSanitize.MatchString(tagVal) {
						return o, errInvalidOptionVal
					}
					o.Tags = append(o.Tags, tagVal)
				}
			}
			continue
		default:
			continue
		}
		if key != "tag_selector" && !reSanitize.MatchString(values[0]) {
			return o, errInvalidOptionVal
		}
	}
	return
}

func getString(m map[string]interface{}, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

// getBool returns a boolean value from a map, converting it from a string if necessary
func getBool(m map[string]interface{}, key string) bool {
	switch v := m[key].(type) {
	case bool:
		return v
	case string:
		return v == "YES" || v == "true" || v == "t" || v == "1"
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	default:
		return false
	}
}

func getEnvValue(envs map[string]string, key string) string {
	if val, exists := envs[key]; exists {
		decoded, err := base64.StdEncoding.DecodeString(val)
		if err != nil {
			return ""
		}
		return string(decoded)
	}
	return ""
}

func getMongoDBFromConnectionString(connStr string) string {
	// Decode the base64-encoded connection string
	decoded, err := base64.StdEncoding.DecodeString(connStr)
	if err != nil {
		return ""
	}
	mongoURL := string(decoded)

	// If the URL doesn't start with "mongodb://", it's not a valid MongoDB URL
	if !strings.HasPrefix(mongoURL, "mongodb://") {
		return ""
	}

	// Parse the URL to extract the database name
	u, err := url.Parse(mongoURL)
	if err != nil {
		return ""
	}

	// The database comes after the first slash in the path
	path := u.Path
	if path == "" || path == "/" {
		return ""
	}

	// Remove the leading slash
	return strings.TrimPrefix(path, "/")
}

func parseDatabaseCommandOutput(output string) ([]string, error) {
	lines := strings.Split(output, "\n")
	var cleanLines []string

	// Remove empty lines and header
	for i, line := range lines {
		line = strings.TrimSpace(line)
		// Skip first line (header)
		if i == 0 || line == "" {
			continue
		}
		// Stop at the first line that starts with a parenthesis
		if strings.HasPrefix(line, "(") {
			break
		}
		cleanLines = append(cleanLines, line)
	}

	return cleanLines, nil
}

// validateDatabaseName returns an error if the database name contains invalid characters
func validateDatabaseName(dbName string) error {
	// Regular expression that allows only:
	// - Letters (a-z, A-Z)
	// - Numbers (0-9)
	// - Underscores (_)
	// - Hyphens (-)
	// - Dots (.)
	// With length between 1 and 128 characters
	re := regexp.MustCompile(`^[a-zA-Z0-9_\-\.]{1,128}$`)

	if !re.MatchString(dbName) {
		return fmt.Errorf("invalid database name. Only alphanumeric characters, underscore, hyphen and dot are allowed with length between 1 and 128 characters")
	}

	return nil
}

func cleanMongoOutput(output string) string {
	// If the string is empty,
	if len(output) == 0 {
		return ""
	}

	output = strings.TrimSpace(output)
	startJSON := -1

	// Addicional protection after TrimSpace
	if len(output) == 0 {
		return ""
	}

	for i, char := range output {
		if char == '[' || char == '{' {
			startJSON = i
			break
		}
	}

	// If don't find the start of JSON, return empty string
	if startJSON < 0 {
		return ""
	}

	// Ensure we don't have a panic with the slice
	if startJSON >= len(output) {
		return ""
	}

	return output[startJSON:]
}

// parseMongoDBColumns parses MongoDB output and returns a slice of ConnectionColumns
func parseMongoDBColumns(output string) ([]openapi.ConnectionColumn, error) {
	originalOutput := output

	output = cleanMongoOutput(output)
	if output == "" {
		if strings.TrimSpace(originalOutput) != "" {
			return nil, fmt.Errorf("failed to parse invalid MongoDB response")
		}
		return []openapi.ConnectionColumn{}, nil
	}

	var result []map[string]interface{}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return nil, fmt.Errorf("failed to parse MongoDB response: %v", err)
	}

	columns := []openapi.ConnectionColumn{}
	for _, row := range result {
		columnName := getString(row, "column_name")
		columnType := getString(row, "column_type")

		if columnName != "" {
			column := openapi.ConnectionColumn{
				Name:     columnName,
				Type:     columnType,
				Nullable: !getBool(row, "not_null"),
			}
			columns = append(columns, column)
		}
	}

	return columns, nil
}

// parseSQLColumns parses SQL output and returns a slice of ConnectionColumns
func parseSQLColumns(output string, connectionType pb.ConnectionType) ([]openapi.ConnectionColumn, error) {
	columns := []openapi.ConnectionColumn{}
	lines := strings.Split(output, "\n")

	// Process each line (skip header)
	startLine := 1
	if connectionType == pb.ConnectionTypeMSSQL {
		// Find the line with dashes for MSSQL
		for i, line := range lines {
			if strings.Contains(line, "----") {
				startLine = i + 1
				break
			}
		}
	}

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if i < startLine || line == "" || strings.HasPrefix(line, "(") {
			continue
		}

		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}

		column := openapi.ConnectionColumn{
			Name:     fields[0],
			Type:     fields[1],
			Nullable: fields[2] != "t" && fields[2] != "1",
		}
		columns = append(columns, column)
	}

	return columns, nil
}

// parseMongoDBTables parses MongoDB output and returns a TablesResponse structure
func parseMongoDBTables(output string) (openapi.TablesResponse, error) {
	response := openapi.TablesResponse{Schemas: []openapi.SchemaInfo{}}

	originalOutput := output

	output = cleanMongoOutput(output)
	if output == "" {
		if strings.TrimSpace(originalOutput) != "" {
			return response, fmt.Errorf("failed to parse invalid MongoDB response")
		}
		return response, nil
	}

	var result []map[string]interface{}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return response, fmt.Errorf("failed to parse MongoDB response: %v", err)
	}

	// Organize tables by schema
	schemaMap := make(map[string][]string)
	for _, row := range result {
		schemaName := getString(row, "schema_name")
		tableName := getString(row, "object_name")

		if schemaName != "" && tableName != "" {
			schemaMap[schemaName] = append(schemaMap[schemaName], tableName)
		}
	}

	// Convert map to response structure
	for schemaName, tables := range schemaMap {
		response.Schemas = append(response.Schemas, openapi.SchemaInfo{
			Name:   schemaName,
			Tables: tables,
		})
	}

	return response, nil
}

// parseSQLTables parses SQL output and returns a TablesResponse structure
func parseSQLTables(output string, connectionType pb.ConnectionType) (openapi.TablesResponse, error) {
	response := openapi.TablesResponse{Schemas: []openapi.SchemaInfo{}}

	lines := strings.Split(output, "\n")
	schemaMap := make(map[string][]string)

	// Process each line (skip header)
	startLine := 1
	if connectionType == pb.ConnectionTypeMSSQL {
		// Find the line with dashes for MSSQL
		for i, line := range lines {
			if strings.Contains(line, "----") {
				startLine = i + 1
				break
			}
		}
	}

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if i < startLine || line == "" || strings.HasPrefix(line, "(") {
			continue
		}

		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}

		schemaName := fields[0]
		objectName := fields[2]

		schemaMap[schemaName] = append(schemaMap[schemaName], objectName)
	}

	// Convert map to response structure
	for schemaName, objects := range schemaMap {
		response.Schemas = append(response.Schemas, openapi.SchemaInfo{
			Name:   schemaName,
			Tables: objects,
		})
	}

	return response, nil
}

// Parse DynamoDB list-tables output
func parseDynamoDBTables(output string) (openapi.TablesResponse, error) {
	var result struct {
		TableNames []string `json:"TableNames"`
	}

	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return openapi.TablesResponse{}, err
	}

	// Create response in expected format
	response := openapi.TablesResponse{
		Schemas: []openapi.SchemaInfo{
			{
				Name:   "default",
				Tables: []string{},
			},
		},
	}

	// Add tables
	for _, tableName := range result.TableNames {
		response.Schemas[0].Tables = append(response.Schemas[0].Tables, tableName)
	}

	return response, nil
}

// Parse DynamoDB describe-table output to extract column information
func parseDynamoDBColumns(output string) ([]openapi.ConnectionColumn, error) {
	var result struct {
		Table struct {
			AttributeDefinitions []struct {
				AttributeName string `json:"AttributeName"`
				AttributeType string `json:"AttributeType"` // S, N, B (string, number, binary)
			} `json:"AttributeDefinitions"`
			KeySchema []struct {
				AttributeName string `json:"AttributeName"`
				KeyType       string `json:"KeyType"` // HASH ou RANGE
			} `json:"KeySchema"`
		} `json:"Table"`
	}

	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return nil, err
	}

	var columns []openapi.ConnectionColumn

	// Convert AttributeDefinitions to expected format
	for _, attr := range result.Table.AttributeDefinitions {
		dataType := "string"
		if attr.AttributeType == "N" {
			dataType = "number"
		} else if attr.AttributeType == "B" {
			dataType = "binary"
		}

		// Check if it's a primary key but we don't need to store the result
		// since we're always setting Nullable to false for key attributes
		for _, key := range result.Table.KeySchema {
			if key.AttributeName == attr.AttributeName {
				// Found a key match - no need to store this information currently
				break
			}
		}

		columns = append(columns, openapi.ConnectionColumn{
			Name:     attr.AttributeName,
			Type:     dataType,
			Nullable: false, // Key attributes are always not null
		})
	}

	return columns, nil
}

// parseCloudWatchTables parses CloudWatch output and returns a TablesResponse structure
func parseCloudWatchTables(output string) (openapi.TablesResponse, error) {
	var result struct {
		LogGroups []struct {
			LogGroupName string `json:"logGroupName"`
		} `json:"logGroups"`
	}

	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return openapi.TablesResponse{}, err
	}

	// Create response in expected format
	response := openapi.TablesResponse{
		Schemas: []openapi.SchemaInfo{
			{
				Name:   "cloudwatch",
				Tables: []string{},
			},
		},
	}

	// Add log groups as "tables"
	for _, logGroup := range result.LogGroups {
		response.Schemas[0].Tables = append(response.Schemas[0].Tables, logGroup.LogGroupName)
	}

	return response, nil
}

func getConnectionCommandOverride(currentConnectionType pb.ConnectionType, connectionCmd []string) []string {
	var cmd []string
	switch currentConnectionType {
	case pb.ConnectionTypeCloudWatch, pb.ConnectionTypeDynamoDB:
		return []string{"bash"}
	case pb.ConnectionTypeMongoDB:
		// Force the execution using the legacy mongo cli
		// It avoids using any wrapper scripts (.e.g: /opt/hoop/bin/mongo) to perform system queries
		if len(connectionCmd) > 1 {
			cmd = append(cmd, "/usr/local/bin/mongo")
			cmd = append(cmd, connectionCmd[1:]...)
		}
	}
	return cmd
}
