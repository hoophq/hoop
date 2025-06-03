package apiconnections

import (
	"net/url"
	"sort"
	"strings"
	"testing"

	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
)

func TestConnectionFilterOptions(t *testing.T) {
	for _, tt := range []struct {
		msg     string
		opts    map[string]string
		want    models.ConnectionFilterOption
		wantErr string
	}{
		{
			msg:  "it must be able to accept all options",
			opts: map[string]string{"type": "database", "subtype": "postgres", "managed_by": "hoopagent", "tags": "prod,devops"},
			want: models.ConnectionFilterOption{Type: "database", SubType: "postgres", ManagedBy: "hoopagent", Tags: []string{"prod", "devops"}},
		},
		{
			msg:  "it must ignore unknown options",
			opts: map[string]string{"unknown_option": "val", "tags.foo.bar": "val"},
			want: models.ConnectionFilterOption{},
		},
		{
			msg:     "it must error with invalid option values",
			opts:    map[string]string{"subtype": "value with space"},
			wantErr: errInvalidOptionVal.Error(),
		},
		{
			msg:     "it must error with invalid option values, special characteres",
			opts:    map[string]string{"subtype": "value&^%$#@"},
			wantErr: errInvalidOptionVal.Error(),
		},
		{
			msg:     "it must error when tag values has invalid option values",
			opts:    map[string]string{"tags": "foo,tag with space"},
			wantErr: errInvalidOptionVal.Error(),
		},
		{
			msg:     "it must error when tag values are empty",
			opts:    map[string]string{"tags": "foo,,,,"},
			wantErr: errInvalidOptionVal.Error(),
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			urlValues := url.Values{}
			for key, val := range tt.opts {
				urlValues[key] = []string{val}
			}
			got, err := validateListOptions(urlValues)
			if err != nil {
				assert.EqualError(t, err, tt.wantErr)
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidateDatabaseName(t *testing.T) {
	tests := []struct {
		name    string
		dbName  string
		wantErr bool
	}{
		{
			name:    "valid name",
			dbName:  "myapp_db",
			wantErr: false,
		},
		{
			name:    "valid name with dots",
			dbName:  "my.database.test",
			wantErr: false,
		},
		{
			name:    "valid name with hyphens",
			dbName:  "my-database-test",
			wantErr: false,
		},
		{
			name:    "empty string",
			dbName:  "",
			wantErr: true,
		},
		{
			name:    "too long name",
			dbName:  strings.Repeat("a", 129),
			wantErr: true,
		},
		{
			name:    "special characters",
			dbName:  "my@database",
			wantErr: true,
		},
		{
			name:    "spaces",
			dbName:  "my database",
			wantErr: true,
		},
		{
			name:    "sql injection attempt",
			dbName:  "db; DROP TABLE users;--",
			wantErr: true,
		},
		{
			name:    "reserved word postgres",
			dbName:  "postgres",
			wantErr: false,
		},
		{
			name:    "reserved word master",
			dbName:  "master",
			wantErr: false,
		},
		{
			name:    "reserved word information_schema",
			dbName:  "information_schema",
			wantErr: false,
		},
		{
			name:    "unicode characters",
			dbName:  "database💾",
			wantErr: true,
		},
		{
			name:    "case insensitive reserved word",
			dbName:  "MASTER",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDatabaseName(tt.dbName)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateDatabaseName() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCleanMongoOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "already valid JSON array",
			input:    `[{"test": "value"}]`,
			expected: `[{"test": "value"}]`,
		},
		{
			name:     "already valid JSON object",
			input:    `{"test": "value"}`,
			expected: `{"test": "value"}`,
		},
		{
			name:     "WriteResult at beginning",
			input:    `WriteResult[{"test": "value"}]`,
			expected: `[{"test": "value"}]`,
		},
		{
			name:     "whitespace at beginning",
			input:    `    [{"test": "value"}]`,
			expected: `[{"test": "value"}]`,
		},
		{
			name:     "WriteResult with spaces",
			input:    `   WriteResult   [{"test": "value"}]`,
			expected: `[{"test": "value"}]`,
		},
		{
			name:     "random text before JSON",
			input:    `some random text here {"test": "value"}`,
			expected: `{"test": "value"}`,
		},
		{
			name:     "WriteResult before object",
			input:    `WriteResult{"test": "value"}`,
			expected: `{"test": "value"}`,
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "text without JSON",
			input:    "WriteResult",
			expected: "",
		},
		{
			name: "multiline JSON with text before",
			input: `WriteResult
					[{
							"test": "value",
							"other": "value2"
					}]`,
			expected: `[{
							"test": "value",
							"other": "value2"
					}]`,
		},
		{
			name:     "only whitespace",
			input:    "   \t\n   ",
			expected: "",
		},
		{
			name:     "special characters",
			input:    "⌘⌥∑œ∑´®†¥¨ˆøπ'",
			expected: "",
		},
		{
			name:     "very long string without JSON",
			input:    strings.Repeat("a", 1000000),
			expected: "",
		},
		{
			name:     "control characters before JSON",
			input:    string([]byte{0x00, 0x01, 0x02}) + `{"test": "value"}`,
			expected: `{"test": "value"}`,
		},
		{
			name:     "unicode string",
			input:    "你好世界[1,2,3]",
			expected: "[1,2,3]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test that the function doesn't panic
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("function caused panic with input: %q, panic: %v", tt.input, r)
				}
			}()

			result := cleanMongoOutput(tt.input)
			if result != tt.expected {
				t.Errorf("\ncleanMongoOutput(%q) =\n%v\nwant:\n%v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseMongoDBTables(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    openapi.TablesResponse
		wantErr bool
	}{
		{
			name:    "empty input",
			input:   "",
			want:    openapi.TablesResponse{Schemas: []openapi.SchemaInfo{}},
			wantErr: false,
		},
		{
			name:    "invalid JSON",
			input:   "invalid json",
			want:    openapi.TablesResponse{Schemas: []openapi.SchemaInfo{}},
			wantErr: true,
		},
		{
			name: "single schema with single table",
			input: `[{
				"schema_name": "admin",
				"object_name": "users"
			}]`,
			want: openapi.TablesResponse{
				Schemas: []openapi.SchemaInfo{
					{
						Name:   "admin",
						Tables: []string{"users"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "single schema with multiple tables",
			input: `[
				{
					"schema_name": "admin",
					"object_name": "users"
				},
				{
					"schema_name": "admin",
					"object_name": "roles"
				}
			]`,
			want: openapi.TablesResponse{
				Schemas: []openapi.SchemaInfo{
					{
						Name:   "admin",
						Tables: []string{"users", "roles"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "multiple schemas with multiple tables",
			input: `[
				{
					"schema_name": "admin",
					"object_name": "users"
				},
				{
					"schema_name": "admin",
					"object_name": "roles"
				},
				{
					"schema_name": "config",
					"object_name": "settings"
				},
				{
					"schema_name": "public",
					"object_name": "products"
				},
				{
					"schema_name": "public",
					"object_name": "orders"
				}
			]`,
			want: openapi.TablesResponse{
				Schemas: []openapi.SchemaInfo{
					{
						Name:   "admin",
						Tables: []string{"users", "roles"},
					},
					{
						Name:   "config",
						Tables: []string{"settings"},
					},
					{
						Name:   "public",
						Tables: []string{"products", "orders"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "with MongoDB output prefix",
			input: `WriteResult
			[
				{
					"schema_name": "test",
					"object_name": "collection1"
				}
			]`,
			want: openapi.TablesResponse{
				Schemas: []openapi.SchemaInfo{
					{
						Name:   "test",
						Tables: []string{"collection1"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "with additional fields",
			input: `[
				{
					"schema_name": "admin",
					"object_name": "users",
					"extra_field": "ignored",
					"another_field": 123
				}
			]`,
			want: openapi.TablesResponse{
				Schemas: []openapi.SchemaInfo{
					{
						Name:   "admin",
						Tables: []string{"users"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "missing schema name",
			input: `[
				{
					"object_name": "users",
					"extra_field": "ignored"
				}
			]`,
			want:    openapi.TablesResponse{Schemas: []openapi.SchemaInfo{}},
			wantErr: false,
		},
		{
			name: "missing object name",
			input: `[
				{
					"schema_name": "admin",
					"extra_field": "ignored"
				}
			]`,
			want:    openapi.TablesResponse{Schemas: []openapi.SchemaInfo{}},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMongoDBTables(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseMongoDBTables() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Para comparar, vamos ordenar os schemas e as tabelas para garantir
				// uma comparação consistente, já que a ordem dos maps pode variar
				sortTablesResponse(&got)
				sortTablesResponse(&tt.want)

				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// sortTablesResponse ordena os schemas e tabelas para garantir comparações consistentes
func sortTablesResponse(resp *openapi.TablesResponse) {
	// Ordena os schemas por nome
	sort.Slice(resp.Schemas, func(i, j int) bool {
		return resp.Schemas[i].Name < resp.Schemas[j].Name
	})

	// Ordena as tabelas de cada schema
	for i := range resp.Schemas {
		sort.Strings(resp.Schemas[i].Tables)
	}
}

func TestParseSQLTables(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		connectionType pb.ConnectionType
		want           openapi.TablesResponse
	}{
		{
			name:           "empty input",
			input:          "",
			connectionType: pb.ConnectionTypePostgres,
			want:           openapi.TablesResponse{Schemas: []openapi.SchemaInfo{}},
		},
		{
			name: "postgres format",
			input: `schema_name	object_type	object_name
public	table	users
public	table	profiles
schema1	table	records
schema2	table	settings`,
			connectionType: pb.ConnectionTypePostgres,
			want: openapi.TablesResponse{
				Schemas: []openapi.SchemaInfo{
					{
						Name:   "public",
						Tables: []string{"users", "profiles"},
					},
					{
						Name:   "schema1",
						Tables: []string{"records"},
					},
					{
						Name:   "schema2",
						Tables: []string{"settings"},
					},
				},
			},
		},
		{
			name: "postgres with private schema",
			input: `schema_name	object_type	object_name
public	table	schema_migrations
public	view	users
public	view	connections
private	table	users
private	table	connections
private	table	orgs`,
			connectionType: pb.ConnectionTypePostgres,
			want: openapi.TablesResponse{
				Schemas: []openapi.SchemaInfo{
					{
						Name:   "public",
						Tables: []string{"schema_migrations", "users", "connections"},
					},
					{
						Name:   "private",
						Tables: []string{"users", "connections", "orgs"},
					},
				},
			},
		},
		{
			name: "mssql format with dashes",
			input: `schema_name	object_type	object_name
-----------	-----------	-----------
dbo	table	customers
dbo	table	orders
sales	table	products`,
			connectionType: pb.ConnectionTypeMSSQL,
			want: openapi.TablesResponse{
				Schemas: []openapi.SchemaInfo{
					{
						Name:   "dbo",
						Tables: []string{"customers", "orders"},
					},
					{
						Name:   "sales",
						Tables: []string{"products"},
					},
				},
			},
		},
		{
			name: "mysql format",
			input: `schema_name	object_type	object_name
app_db	table	users
app_db	table	roles
log_db	table	events`,
			connectionType: pb.ConnectionTypeMySQL,
			want: openapi.TablesResponse{
				Schemas: []openapi.SchemaInfo{
					{
						Name:   "app_db",
						Tables: []string{"users", "roles"},
					},
					{
						Name:   "log_db",
						Tables: []string{"events"},
					},
				},
			},
		},
		{
			name: "with row count at end",
			input: `schema_name	object_type	object_name
public	table	users
public	table	roles
(2 rows)`,
			connectionType: pb.ConnectionTypePostgres,
			want: openapi.TablesResponse{
				Schemas: []openapi.SchemaInfo{
					{
						Name:   "public",
						Tables: []string{"users", "roles"},
					},
				},
			},
		},
		{
			name: "with insufficient columns",
			input: `schema_name	object_name
public	users
schema1	table1`,
			connectionType: pb.ConnectionTypePostgres,
			want:           openapi.TablesResponse{Schemas: []openapi.SchemaInfo{}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSQLTables(tt.input, tt.connectionType)
			if err != nil {
				t.Errorf("parseSQLTables() error = %v", err)
				return
			}

			// Ordenar para comparação consistente
			sortTablesResponse(&got)
			sortTablesResponse(&tt.want)

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseMongoDBColumns(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []openapi.ConnectionColumn
		wantErr bool
	}{
		{
			name:    "empty input",
			input:   "",
			want:    []openapi.ConnectionColumn{},
			wantErr: false,
		},
		{
			name:    "invalid JSON",
			input:   "invalid json",
			want:    nil,
			wantErr: true,
		},
		{
			name: "valid input with single column",
			input: `[{
				"column_name": "id",
				"column_type": "objectId",
				"not_null": true
			}]`,
			want: []openapi.ConnectionColumn{
				{
					Name:     "id",
					Type:     "objectId",
					Nullable: false,
				},
			},
			wantErr: false,
		},
		{
			name: "valid input with multiple columns",
			input: `[
				{
					"column_name": "id",
					"column_type": "objectId",
					"not_null": true
				},
				{
					"column_name": "name",
					"column_type": "string",
					"not_null": false
				},
				{
					"column_name": "email",
					"column_type": "string",
					"not_null": true
				}
			]`,
			want: []openapi.ConnectionColumn{
				{
					Name:     "id",
					Type:     "objectId",
					Nullable: false,
				},
				{
					Name:     "name",
					Type:     "string",
					Nullable: true,
				},
				{
					Name:     "email",
					Type:     "string",
					Nullable: false,
				},
			},
			wantErr: false,
		},
		{
			name: "input with extra fields",
			input: `[{
				"column_name": "id",
				"column_type": "objectId",
				"not_null": true,
				"extra_field": "value"
			}]`,
			want: []openapi.ConnectionColumn{
				{
					Name:     "id",
					Type:     "objectId",
					Nullable: false,
				},
			},
			wantErr: false,
		},
		{
			name: "input with text before JSON",
			input: `WriteResult
			[{
				"column_name": "id",
				"column_type": "objectId",
				"not_null": true
			}]`,
			want: []openapi.ConnectionColumn{
				{
					Name:     "id",
					Type:     "objectId",
					Nullable: false,
				},
			},
			wantErr: false,
		},
		{
			name: "missing column name",
			input: `[{
				"column_type": "objectId",
				"not_null": true
			}]`,
			want:    []openapi.ConnectionColumn{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMongoDBColumns(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseMongoDBColumns() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestParseSQLColumns(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		connectionType pb.ConnectionType
		want           []openapi.ConnectionColumn
	}{
		{
			name:           "empty input",
			input:          "",
			connectionType: pb.ConnectionTypePostgres,
			want:           []openapi.ConnectionColumn{},
		},
		{
			name: "postgres format",
			input: `column_name	column_type	not_null
id	integer	t
name	varchar(255)	f
email	varchar(255)	t`,
			connectionType: pb.ConnectionTypePostgres,
			want: []openapi.ConnectionColumn{
				{
					Name:     "id",
					Type:     "integer",
					Nullable: false,
				},
				{
					Name:     "name",
					Type:     "varchar(255)",
					Nullable: true,
				},
				{
					Name:     "email",
					Type:     "varchar(255)",
					Nullable: false,
				},
			},
		},
		{
			name: "mysql format",
			input: `column_name	column_type	not_null
id	int	1
name	varchar(255)	0
email	varchar(255)	1`,
			connectionType: pb.ConnectionTypeMySQL,
			want: []openapi.ConnectionColumn{
				{
					Name:     "id",
					Type:     "int",
					Nullable: false,
				},
				{
					Name:     "name",
					Type:     "varchar(255)",
					Nullable: true,
				},
				{
					Name:     "email",
					Type:     "varchar(255)",
					Nullable: false,
				},
			},
		},
		{
			name: "mssql format with header dash line",
			input: `column_name	column_type	not_null
-----------	-----------	---------
id	int	1
name	varchar(255)	0
email	varchar(255)	1`,
			connectionType: pb.ConnectionTypeMSSQL,
			want: []openapi.ConnectionColumn{
				{
					Name:     "id",
					Type:     "int",
					Nullable: false,
				},
				{
					Name:     "name",
					Type:     "varchar(255)",
					Nullable: true,
				},
				{
					Name:     "email",
					Type:     "varchar(255)",
					Nullable: false,
				},
			},
		},
		{
			name: "with parenthesis end line",
			input: `column_name	column_type	not_null
id	int	1
name	varchar(255)	0
(3 rows)`,
			connectionType: pb.ConnectionTypePostgres,
			want: []openapi.ConnectionColumn{
				{
					Name:     "id",
					Type:     "int",
					Nullable: false,
				},
				{
					Name:     "name",
					Type:     "varchar(255)",
					Nullable: true,
				},
			},
		},
		{
			name: "with fewer fields than expected",
			input: `column_name	column_type
id	int
name	varchar(255)`,
			connectionType: pb.ConnectionTypePostgres,
			want:           []openapi.ConnectionColumn{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSQLColumns(tt.input, tt.connectionType)
			if err != nil {
				t.Errorf("parseSQLColumns() error = %v", err)
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseDynamoDBTables(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    openapi.TablesResponse
		wantErr bool
	}{
		{
			name:    "empty input",
			input:   "",
			want:    openapi.TablesResponse{},
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			input:   "invalid json",
			want:    openapi.TablesResponse{},
			wantErr: true,
		},
		{
			name:  "no tables",
			input: `{"TableNames": []}`,
			want: openapi.TablesResponse{
				Schemas: []openapi.SchemaInfo{
					{
						Name:   "default",
						Tables: []string{},
					},
				},
			},
			wantErr: false,
		},
		{
			name:  "single table",
			input: `{"TableNames": ["CustomerBookmark"]}`,
			want: openapi.TablesResponse{
				Schemas: []openapi.SchemaInfo{
					{
						Name:   "default",
						Tables: []string{"CustomerBookmark"},
					},
				},
			},
			wantErr: false,
		},
		{
			name:  "multiple tables",
			input: `{"TableNames": ["CustomerBookmark", "Orders", "Products"]}`,
			want: openapi.TablesResponse{
				Schemas: []openapi.SchemaInfo{
					{
						Name:   "default",
						Tables: []string{"CustomerBookmark", "Orders", "Products"},
					},
				},
			},
			wantErr: false,
		},
		{
			name:  "with extra fields",
			input: `{"TableNames": ["CustomerBookmark"], "Count": 1, "ScannedCount": 1}`,
			want: openapi.TablesResponse{
				Schemas: []openapi.SchemaInfo{
					{
						Name:   "default",
						Tables: []string{"CustomerBookmark"},
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDynamoDBTables(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseDynamoDBTables() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestParseDynamoDBColumns(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []openapi.ConnectionColumn
		wantErr bool
	}{
		{
			name:    "empty input",
			input:   "",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			input:   "invalid json",
			want:    nil,
			wantErr: true,
		},
		{
			name: "simple hash key table",
			input: `{
				"Table": {
					"AttributeDefinitions": [
						{
							"AttributeName": "id",
							"AttributeType": "S"
						}
					],
					"KeySchema": [
						{
							"AttributeName": "id",
							"KeyType": "HASH"
						}
					]
				}
			}`,
			want: []openapi.ConnectionColumn{
				{
					Name:     "id",
					Type:     "string",
					Nullable: false,
				},
			},
			wantErr: false,
		},
		{
			name: "composite key table",
			input: `{
				"Table": {
					"AttributeDefinitions": [
						{
							"AttributeName": "customerId",
							"AttributeType": "S"
						},
						{
							"AttributeName": "sk",
							"AttributeType": "S"
						}
					],
					"KeySchema": [
						{
							"AttributeName": "customerId",
							"KeyType": "HASH"
						},
						{
							"AttributeName": "sk",
							"KeyType": "RANGE"
						}
					]
				}
			}`,
			want: []openapi.ConnectionColumn{
				{
					Name:     "customerId",
					Type:     "string",
					Nullable: false,
				},
				{
					Name:     "sk",
					Type:     "string",
					Nullable: false,
				},
			},
			wantErr: false,
		},
		{
			name: "different attribute types",
			input: `{
				"Table": {
					"AttributeDefinitions": [
						{
							"AttributeName": "id",
							"AttributeType": "S"
						},
						{
							"AttributeName": "age",
							"AttributeType": "N"
						},
						{
							"AttributeName": "data",
							"AttributeType": "B"
						}
					],
					"KeySchema": [
						{
							"AttributeName": "id",
							"KeyType": "HASH"
						}
					]
				}
			}`,
			want: []openapi.ConnectionColumn{
				{
					Name:     "id",
					Type:     "string",
					Nullable: false,
				},
				{
					Name:     "age",
					Type:     "number",
					Nullable: false,
				},
				{
					Name:     "data",
					Type:     "binary",
					Nullable: false,
				},
			},
			wantErr: false,
		},
		{
			name: "full table description",
			input: `{
				"Table": {
					"AttributeDefinitions": [
						{
							"AttributeName": "customerId",
							"AttributeType": "S"
						},
						{
							"AttributeName": "sk",
							"AttributeType": "S"
						}
					],
					"TableName": "CustomerBookmark",
					"KeySchema": [
						{
							"AttributeName": "customerId",
							"KeyType": "HASH"
						},
						{
							"AttributeName": "sk",
							"KeyType": "RANGE"
						}
					],
					"TableStatus": "ACTIVE",
					"CreationDateTime": "2025-06-03T18:26:55.094000+00:00",
					"ProvisionedThroughput": {
						"NumberOfDecreasesToday": 0,
						"ReadCapacityUnits": 1,
						"WriteCapacityUnits": 1
					},
					"TableSizeBytes": 0,
					"ItemCount": 0,
					"TableArn": "arn:aws:dynamodb:us-east-1:123456789012:table/CustomerBookmark",
					"TableId": "6948fd33-0433-428c-8a83-0d18d8d0b34c",
					"GlobalSecondaryIndexes": [
						{
							"IndexName": "ByEmail",
							"KeySchema": [
								{
									"AttributeName": "email",
									"KeyType": "HASH"
								}
							],
							"Projection": {
								"ProjectionType": "ALL"
							}
						}
					]
				}
			}`,
			want: []openapi.ConnectionColumn{
				{
					Name:     "customerId",
					Type:     "string",
					Nullable: false,
				},
				{
					Name:     "sk",
					Type:     "string",
					Nullable: false,
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDynamoDBColumns(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseDynamoDBColumns() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}
