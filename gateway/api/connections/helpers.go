package apiconnections

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
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

// ApplyDefaultsToModel fills subtype-specific runtime defaults (command and env vars)
// on a connection model in place. Idempotent: existing command and env keys are preserved.
// Mirrors setConnectionDefaults for callers that work directly with models.Connection
// (e.g. the MCP server).
func ApplyDefaultsToModel(conn *models.Connection) {
	if conn.Envs == nil {
		conn.Envs = map[string]string{}
	}
	hasMongoConnStr := conn.Envs["envvar:CONNECTION_STRING"] != ""
	defaultCmd, defaultEnvs := GetConnectionDefaults(conn.Type, conn.SubType.String, hasMongoConnStr)
	if len(conn.Command) == 0 {
		conn.Command = defaultCmd
	}
	for k, v := range defaultEnvs {
		if _, has := conn.Envs[k]; !has {
			conn.Envs[k] = fmt.Sprintf("%v", v)
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

func CoerceToMapString(src map[string]any) map[string]string {
	dst := map[string]string{}
	for k, v := range src {
		dst[k] = fmt.Sprintf("%v", v)
	}
	return dst
}

func CoerceToMapNullableString(src map[string]any) map[string]*string {
	dst := map[string]*string{}
	for k, v := range src {
		if v == nil {
			dst[k] = nil
		} else {
			strVal := fmt.Sprintf("%v", v)
			dst[k] = &strVal
		}
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

func validatePatchConnectionRequest(req openapi.ConnectionPatch) error {
	errors := []string{}
	// TODO: deprecated
	if req.Tags != nil {
		for _, val := range *req.Tags {
			if !tagsValRe.MatchString(val) {
				errors = append(errors, "tags: values must contain between 1 and 128 alphanumeric characters, it may include (-), (_) or (.) characters")
			}
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("%s", strings.Join(errors, "; "))
	}

	if req.ConnectionTags != nil && len(*req.ConnectionTags) > 10 {
		return fmt.Errorf("max tag association reached (10)")
	}

	if req.ConnectionTags != nil {
		for key, val := range *req.ConnectionTags {
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
	}

	if len(errors) > 0 {
		return fmt.Errorf("%s", strings.Join(errors, "; "))
	}
	return nil
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
		return fmt.Errorf("%s", strings.Join(errors, "; "))
	}

	if len(req.ConnectionTags) > 10 {
		return fmt.Errorf("max tag association reached (10)")
	}

	if req.MinReviewApprovals != nil && *req.MinReviewApprovals <= 0 {
		return fmt.Errorf("min review approvals must be greater than 0 or null")
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
		return fmt.Errorf("%s", strings.Join(errors, "; "))
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
		case "search":
			o.Search = strings.TrimLeft(values[0], " ")
			continue
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
		case "resource_name":
			o.ResourceName = values[0]
			continue
		case "attribute":
			if len(values[0]) > 0 {
				for _, attr := range strings.Split(values[0], ",") {
					if !reSanitize.MatchString(attr) {
						return o, errInvalidOptionVal
					}
					o.Attributes = append(o.Attributes, attr)
				}
			}
			continue
		case "name":
			o.Name = strings.TrimLeft(values[0], " ")
			continue
		case "connection_ids":
			if len(values[0]) > 0 {
				for _, connID := range strings.Split(values[0], ",") {
					connID = strings.TrimSpace(connID)
					if connID == "" {
						continue
					}
					// Validate UUID format
					if _, err := uuid.Parse(connID); err != nil {
						return o, fmt.Errorf("invalid connection ID format: %s", connID)
					}
					o.ConnectionIDs = append(o.ConnectionIDs, connID)
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
		if i == 0 || line == "" || line == "----" {
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

// nameDialect selects how strictly validateObjectName treats a table,
// collection or schema name. The two policies differ because the two paths
// have different safety properties, not because one is a relaxed version of
// the other.
type nameDialect int

const (
	// dialectInterpolated is for names spliced into a SQL string literal or a
	// shell word (every connection type whose introspection query is built
	// with fmt.Sprintf). Nothing stands between the input and the query text,
	// so only an allowlist is safe.
	dialectInterpolated nameDialect = iota
	// dialectData is for names that travel as a JSON value inside a params
	// document (MongoDB, via gateway/mongoscript). Structure already prevents
	// injection there, so the check only bounds length and rejects characters
	// that would break the transport.
	dialectData
)

const objectNameMaxLen = 120

// objectNameInterpolatedRe mirrors validateDatabaseName's spirit but also
// allows spaces, which real table names use. It deliberately does NOT allow a
// quote, a backslash, a backtick, a semicolon or a parenthesis: those are the
// characters that let a name escape a SQL string literal or a shell word.
var objectNameInterpolatedRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.\- ]{0,119}$`)

// objectNameDialectFor reports how a table/collection/schema name reaches the
// generated script for a connection type.
//
// MongoDB is the only type whose introspection scripts carry the name as JSON
// data (gateway/mongoscript); every other type interpolates it into SQL or a
// shell word, so it gets the strict allowlist.
func objectNameDialectFor(connType pb.ConnectionType) nameDialect {
	// Every type interpolates today, MongoDB included: getMongoDBColumnsQuery
	// still splices the name into generated JavaScript. MongoDB flips to
	// dialectData only when its scripts move to the gateway/mongoscript params
	// layer, where the name travels as a JSON value. Flipping it earlier would
	// leave the JS interpolation unguarded, which is the hole this function
	// exists to close.
	_ = connType
	return dialectInterpolated
}

// validateObjectName returns an error if a table, collection or schema name
// cannot be safely carried to the agent by the given dialect.
//
// A rejection here makes an oddly-named object unbrowsable in the schema
// explorer. That is deliberate: the alternative is interpolating an
// attacker-chosen string into generated SQL, JavaScript or a shell command on
// a path that loads no plugins (no review, no audit, no DLP, no guardrails --
// see gateway/transport/streamclient/pluginruntime.go). A user who needs such
// an object can still query it from the editor, which runs through the full
// plugin chain.
func validateObjectName(name string, d nameDialect) error {
	if name == "" {
		return fmt.Errorf("invalid name: must not be empty")
	}
	if len(name) > objectNameMaxLen {
		return fmt.Errorf("invalid name: must be at most %d characters", objectNameMaxLen)
	}
	switch d {
	case dialectData:
		// MongoDB permits almost any byte in a collection name. Only reject
		// what would break the JSON params document or the log line: control
		// characters, and the two line terminators that are legal inside a
		// JSON string but terminate a JavaScript string literal in ES5.
		for _, r := range name {
			if r < 0x20 || r == 0x7f || r == '\u2028' || r == '\u2029' {
				return fmt.Errorf("invalid name: must not contain control characters")
			}
		}
		return nil
	default:
		if !objectNameInterpolatedRe.MatchString(name) {
			return fmt.Errorf("invalid name. Only alphanumeric characters, underscore, hyphen, dot and space are allowed, starting with an alphanumeric or underscore, with length between 1 and %d characters", objectNameMaxLen)
		}
		return nil
	}
}

// cleanMongoOutput extracts the first JSON value from a mongo shell response.
//
// The shell interleaves its prompt with the script output, and a replica set
// prompt carries brackets of its own, so the first '[' in the stream is not
// necessarily the start of the payload:
//
//	mongodb [primary] lyric> [{"database_name":"lyric"}]
//
// Every '[' or '{' is tried in turn until one decodes, and only that value is
// returned, which also drops the prompt the shell writes after it.
func cleanMongoOutput(output string) string {
	output = strings.TrimSpace(output)

	// A response with many brackets but no valid value costs one decode per
	// candidate, so the scan is bounded. The shell echoes a prompt per script
	// line and every replica set prompt carries a "[primary]", which puts the
	// real floor at the longest script (72 lines today); the cap is an order of
	// magnitude above that and only ever trips on pathological output.
	const maxCandidates = 1024

	attempts := 0
	for i, char := range output {
		if char != '[' && char != '{' {
			continue
		}
		attempts++
		if attempts > maxCandidates {
			return ""
		}

		decoder := json.NewDecoder(strings.NewReader(output[i:]))
		var value json.RawMessage
		if err := decoder.Decode(&value); err == nil {
			return output[i : i+int(decoder.InputOffset())]
		}
	}

	return ""
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

func upsertConnectionAttributes(ctx *storagev2.Context, connectionName string, attributeNames []string) error {
	orgID := uuid.MustParse(ctx.OrgID)
	return models.UpsertConnectionAttributes(models.DB, orgID, connectionName, attributeNames)
}
