package apiconnections

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pgrest"
	pgplugins "github.com/hoophq/hoop/gateway/pgrest/plugins"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

var tagsValRe, _ = regexp.Compile(`^[a-zA-Z0-9_]+(?:[-\.]?[a-zA-Z0-9_]+){0,128}$`)

func accessControlAllowed(ctx pgrest.Context) (func(connName string) bool, error) {
	p, err := pgplugins.New().FetchOne(ctx, plugintypes.PluginAccessControlName)
	if err != nil {
		return nil, err
	}
	if p == nil || ctx.IsAdmin() {
		return func(_ string) bool { return true }, nil
	}

	return func(connName string) bool {
		for _, c := range p.Connections {
			if c.Name == connName {
				for _, userGroup := range ctx.GetUserGroups() {
					if allow := slices.Contains(c.Config, userGroup); allow {
						return allow
					}
				}
				return false
			}
		}
		return false
	}, nil
}

func setConnectionDefaults(req *openapi.Connection) {
	if req.Secrets == nil {
		req.Secrets = map[string]any{}
	}
	var defaultCommand []string
	defaultEnvVars := map[string]any{}
	switch pb.ToConnectionType(req.Type, req.SubType) {
	case pb.ConnectionTypePostgres:
		defaultCommand = []string{"psql", "-v", "ON_ERROR_STOP=1", "-A", "-F\t", "-P", "pager=off", "-h", "$HOST", "-U", "$USER", "--port=$PORT", "$DB"}
	case pb.ConnectionTypeMySQL:
		defaultCommand = []string{"mysql", "-h$HOST", "-u$USER", "--port=$PORT", "-D$DB"}
	case pb.ConnectionTypeMSSQL:
		defaultEnvVars["envvar:INSECURE"] = base64.StdEncoding.EncodeToString([]byte(`false`))
		defaultCommand = []string{
			"sqlcmd", "--exit-on-error", "--trim-spaces", "-s\t", "-r",
			"-S$HOST:$PORT", "-U$USER", "-d$DB", "-i/dev/stdin"}
	case pb.ConnectionTypeOracleDB:
		defaultEnvVars["envvar:LD_LIBRARY_PATH"] = base64.StdEncoding.EncodeToString([]byte(`/opt/oracle/instantclient_19_24`))
		defaultCommand = []string{"sqlplus", "-s", "$USER/$PASS@$HOST:$PORT/$SID"}
	case pb.ConnectionTypeMongoDB:
		defaultEnvVars["envvar:OPTIONS"] = base64.StdEncoding.EncodeToString([]byte(`tls=true`))
		defaultEnvVars["envvar:PORT"] = base64.StdEncoding.EncodeToString([]byte(`27017`))
		defaultCommand = []string{"mongo", "mongodb://$USER:$PASS@$HOST:$PORT/?$OPTIONS", "--quiet"}
		if connStr, ok := req.Secrets["envvar:CONNECTION_STRING"]; ok && connStr != nil {
			defaultEnvVars = nil
			defaultCommand = []string{"mongo", "$CONNECTION_STRING", "--quiet"}
		}
	}

	if len(req.Command) == 0 {
		req.Command = defaultCommand
	}

	for key, val := range defaultEnvVars {
		if _, isset := req.Secrets[key]; !isset {
			req.Secrets[key] = val
		}
	}
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
	for _, val := range req.Tags {
		if !tagsValRe.MatchString(val) {
			errors = append(errors, "tags: values must contain between 1 and 128 alphanumeric characters, it may include (-), (_) or (.) characters")
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
		if !reSanitize.MatchString(values[0]) {
			return o, errInvalidOptionVal
		}
	}
	return
}

func getAccessToken(c *gin.Context) string {
	tokenHeader := c.GetHeader("authorization")
	apiKey := c.GetHeader("Api-Key")
	if apiKey != "" {
		return apiKey
	}
	tokenParts := strings.Split(tokenHeader, " ")
	if len(tokenParts) > 1 {
		return tokenParts[1]
	}
	return ""
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

// parseMongoDBSchema process the raw output from the command and organize it into a SchemaResponse structure
func parseMongoDBSchema(output string) (openapi.ConnectionSchemaResponse, error) {
	var mongoResult []map[string]interface{}
	if err := json.Unmarshal([]byte(output), &mongoResult); err != nil {
		return openapi.ConnectionSchemaResponse{}, fmt.Errorf("failed to parse MongoDB output: %v", err)
	}

	response := openapi.ConnectionSchemaResponse{}
	schemaMap := make(map[string]*openapi.ConnectionSchema)

	for _, row := range mongoResult {
		schemaName := getString(row, "schema_name")
		objectName := getString(row, "object_name")

		// Get or create schema
		schema, exists := schemaMap[schemaName]
		if !exists {
			schema = &openapi.ConnectionSchema{Name: schemaName}
			schemaMap[schemaName] = schema
		}

		// Find or create table
		var table *openapi.ConnectionTable
		for i := range schema.Tables {
			if schema.Tables[i].Name == objectName {
				table = &schema.Tables[i]
				break
			}
		}
		if table == nil {
			schema.Tables = append(schema.Tables, openapi.ConnectionTable{Name: objectName})
			table = &schema.Tables[len(schema.Tables)-1]
		}

		// Add column
		column := openapi.ConnectionColumn{
			Name:         getString(row, "column_name"),
			Type:         getString(row, "column_type"),
			Nullable:     !getBool(row, "not_null"),
			DefaultValue: getString(row, "column_default"),
			IsPrimaryKey: getBool(row, "is_primary_key"),
			IsForeignKey: getBool(row, "is_foreign_key"),
		}
		table.Columns = append(table.Columns, column)

		// Add index if present
		if indexName := getString(row, "index_name"); indexName != "" {
			found := false
			for _, idx := range table.Indexes {
				if idx.Name == indexName {
					found = true
					break
				}
			}
			if !found {
				index := openapi.ConnectionIndex{
					Name:      indexName,
					IsUnique:  getBool(row, "index_is_unique"),
					IsPrimary: getBool(row, "index_is_primary"),
				}
				if cols := getString(row, "index_columns"); cols != "" {
					index.Columns = strings.Split(cols, ",")
				}
				table.Indexes = append(table.Indexes, index)
			}
		}
	}

	// Convert map to slice
	for _, schema := range schemaMap {
		response.Schemas = append(response.Schemas, *schema)
	}

	// fmt.Printf("response parseMongoDBSchema", response)
	return response, nil
}

// parseSchemaOutput process the raw output from the command and organize it into a SchemaResponse structure
func parseSQLSchema(output string, connType pb.ConnectionType) (openapi.ConnectionSchemaResponse, error) {
	lines := strings.Split(output, "\n")
	var result []map[string]interface{}

	// MSSQL has a different output format
	startLine := 0
	if connType == pb.ConnectionTypeMSSQL {
		startLine = 2 // Skip header and dashes for MSSQL
	}

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if i <= startLine || line == "" || strings.HasPrefix(line, "(") {
			continue
		}

		fields := strings.Split(line, "\t")
		if len(fields) < 12 {
			continue
		}

		row := map[string]interface{}{
			"schema_name":    fields[0],
			"object_type":    fields[1],
			"object_name":    fields[2],
			"column_name":    fields[3],
			"column_type":    fields[4],
			"not_null":       fields[5] == "t" || fields[5] == "1",
			"column_default": fields[6],
			"is_primary_key": fields[7] == "t" || fields[7] == "1",
			"is_foreign_key": fields[8] == "t" || fields[8] == "1",
		}

		if len(fields) > 9 && fields[9] != "" {
			row["index_name"] = fields[9]
			row["index_columns"] = strings.Split(fields[10], ",")
			row["index_is_unique"] = fields[11] == "t" || fields[11] == "1"
			row["index_is_primary"] = len(fields) > 12 && (fields[12] == "t" || fields[12] == "1")
		}

		result = append(result, row)
	}

	return organizeSchemaResponse(result), nil
}

// organizeSchemaResponse organizes the raw output into a SchemaResponse structure
func organizeSchemaResponse(rows []map[string]interface{}) openapi.ConnectionSchemaResponse {
	response := openapi.ConnectionSchemaResponse{Schemas: []openapi.ConnectionSchema{}}
	schemaMap := make(map[string]*openapi.ConnectionSchema)

	for _, row := range rows {
		schemaName := row["schema_name"].(string)
		objectType := row["object_type"].(string)
		objectName := row["object_name"].(string)

		// Get or create schema
		schema, exists := schemaMap[schemaName]
		if !exists {
			schema = &openapi.ConnectionSchema{Name: schemaName}
			schemaMap[schemaName] = schema
		}

		column := openapi.ConnectionColumn{
			Name:         row["column_name"].(string),
			Type:         row["column_type"].(string),
			Nullable:     !row["not_null"].(bool),
			DefaultValue: getString(row, "column_default"),
			IsPrimaryKey: row["is_primary_key"].(bool),
			IsForeignKey: row["is_foreign_key"].(bool),
		}

		if objectType == "table" {
			// Find or create table
			var table *openapi.ConnectionTable
			for i := range schema.Tables {
				if schema.Tables[i].Name == objectName {
					table = &schema.Tables[i]
					break
				}
			}
			if table == nil {
				schema.Tables = append(schema.Tables, openapi.ConnectionTable{Name: objectName})
				table = &schema.Tables[len(schema.Tables)-1]
			}

			// Add column
			table.Columns = append(table.Columns, column)

			// Add index if present
			if indexName, ok := row["index_name"].(string); ok && indexName != "" {
				found := false
				for _, idx := range table.Indexes {
					if idx.Name == indexName {
						found = true
						break
					}
				}
				if !found {
					table.Indexes = append(table.Indexes, openapi.ConnectionIndex{
						Name:      indexName,
						Columns:   row["index_columns"].([]string),
						IsUnique:  row["index_is_unique"].(bool),
						IsPrimary: row["index_is_primary"].(bool),
					})
				}
			}
		} else {
			// Find or create view
			var view *openapi.ConnectionView
			for i := range schema.Views {
				if schema.Views[i].Name == objectName {
					view = &schema.Views[i]
					break
				}
			}
			if view == nil {
				schema.Views = append(schema.Views, openapi.ConnectionView{Name: objectName})
				view = &schema.Views[len(schema.Views)-1]
			}

			view.Columns = append(view.Columns, column)
		}
	}

	// Convert map to slice
	for _, schema := range schemaMap {
		response.Schemas = append(response.Schemas, *schema)
	}

	return response
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

	// Some databases don't allow names starting with numbers
	if unicode.IsDigit(rune(dbName[0])) {
		return fmt.Errorf("database name cannot start with a number")
	}

	// Check common reserved words
	reservedWords := []string{
		"master", "tempdb", "model", "msdb", // SQL Server
		"postgres", "template0", "template1", // PostgreSQL
		"mysql", "information_schema", "performance_schema", // MySQL
	}

	dbNameLower := strings.ToLower(dbName)
	for _, word := range reservedWords {
		if dbNameLower == word {
			return fmt.Errorf("database name cannot be a reserved word: %s", word)
		}
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
