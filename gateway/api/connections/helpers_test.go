package apiconnections

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pgrest"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/stretchr/testify/assert"
)

type clientFunc func(req *http.Request) (*http.Response, error)

func (f clientFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func createTestServer(plConn []*pgrest.PluginConnection) clientFunc {
	return clientFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/plugins":
			body := `{"id": "", "org_id": "", "name": "access_control"}`
			return &http.Response{
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(body)),
			}, nil
		case "/plugin_connections":
			pluginConnJson, _ := json.Marshal(plConn)
			return &http.Response{
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBuffer(pluginConnJson)),
			}, nil
		}
		return &http.Response{
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(bytes.NewBufferString(`{"msg": "test not implemented"}`)),
		}, nil

	})
}

func TestAccessControlAllowed(t *testing.T) {
	u, _ := url.Parse("http://localhost:3000")
	pgrest.WithBaseURL(u)
	for _, tt := range []struct {
		msg                string
		allow              bool
		wantConnectionName string
		groups             []string
		fakeClient         clientFunc
	}{
		{
			msg:        "it should allow access to admin users",
			allow:      true,
			groups:     []string{types.GroupAdmin},
			fakeClient: createTestServer([]*pgrest.PluginConnection{}),
		},
		{
			msg:                "it should allow access to users in the allowed groups and with the allowed connection",
			allow:              true,
			wantConnectionName: "bash",
			fakeClient: createTestServer([]*pgrest.PluginConnection{
				{ConnectionConfig: []string{"sre"}, Connection: pgrest.Connection{Name: "bash"}},
			}),
			groups: []string{"sre"},
		},
		{
			msg:                "it should allow access when the user has multiple groups and one of them is allowed",
			allow:              true,
			wantConnectionName: "bash",
			fakeClient: createTestServer([]*pgrest.PluginConnection{
				{ConnectionConfig: []string{"support"}, Connection: pgrest.Connection{Name: "bash"}},
			}),
			groups: []string{"sre", "support", "devops"},
		},
		{
			msg:                "it should deny access if the connection is not found",
			allow:              false,
			wantConnectionName: "bash-not-found",
			fakeClient: createTestServer([]*pgrest.PluginConnection{
				{ConnectionConfig: []string{"sre"}, Connection: pgrest.Connection{Name: "bash"}},
			}),
			groups: []string{"sre"},
		},
		{
			msg:                "it should deny access if the groups does not match",
			allow:              false,
			wantConnectionName: "bash",
			fakeClient: createTestServer([]*pgrest.PluginConnection{
				{ConnectionConfig: []string{"sre"}, Connection: pgrest.Connection{Name: "bash"}},
			}),
			groups: []string{""},
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			pgrest.WithHttpClient(tt.fakeClient)
			ctx := storagev2.NewOrganizationContext("").WithUserInfo("", "", "", "", tt.groups)
			allowed, err := accessControlAllowed(ctx)
			if err != nil {
				t.Fatalf("did not expect error, got %v", err)
			}
			got := allowed(tt.wantConnectionName)
			if got != tt.allow {
				t.Errorf("expected %v, got %v", tt.allow, got)
			}
		})
	}
}

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

func TestParseMongoDBSchema(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    openapi.ConnectionSchemaResponse
		wantErr bool
	}{
		{
			name: "basic schema with single table and column",
			input: `[{
							"schema_name": "testdb",
							"object_name": "users",
							"column_name": "id",
							"column_type": "objectId",
							"not_null": true,
							"column_default": null,
							"is_primary_key": true,
							"is_foreign_key": false
					}]`,
			want: openapi.ConnectionSchemaResponse{
				Schemas: []openapi.ConnectionSchema{
					{
						Name: "testdb",
						Tables: []openapi.ConnectionTable{
							{
								Name: "users",
								Columns: []openapi.ConnectionColumn{
									{
										Name:     "id",
										Type:     "objectId",
										Nullable: false,
									},
								},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "schema with table containing indexes",
			input: `[{
							"schema_name": "testdb",
							"object_name": "users",
							"column_name": "email",
							"column_type": "string",
							"not_null": true,
							"column_default": null,
							"is_primary_key": false,
							"is_foreign_key": false,
							"index_name": "email_idx",
							"index_columns": "email",
							"index_is_unique": true,
							"index_is_primary": false
					}]`,
			want: openapi.ConnectionSchemaResponse{
				Schemas: []openapi.ConnectionSchema{
					{
						Name: "testdb",
						Tables: []openapi.ConnectionTable{
							{
								Name: "users",
								Columns: []openapi.ConnectionColumn{
									{
										Name:     "email",
										Type:     "string",
										Nullable: false,
									},
								},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "schema with multiple tables and columns",
			input: `[
							{
									"schema_name": "testdb",
									"object_name": "users",
									"column_name": "id",
									"column_type": "objectId",
									"not_null": true,
									"is_primary_key": true,
									"is_foreign_key": false
							},
							{
									"schema_name": "testdb",
									"object_name": "users",
									"column_name": "name",
									"column_type": "string",
									"not_null": false,
									"is_primary_key": false,
									"is_foreign_key": false
							},
							{
									"schema_name": "testdb",
									"object_name": "posts",
									"column_name": "id",
									"column_type": "objectId",
									"not_null": true,
									"is_primary_key": true,
									"is_foreign_key": false
							}
					]`,
			want: openapi.ConnectionSchemaResponse{
				Schemas: []openapi.ConnectionSchema{
					{
						Name: "testdb",
						Tables: []openapi.ConnectionTable{
							{
								Name: "users",
								Columns: []openapi.ConnectionColumn{
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
								},
							},
							{
								Name: "posts",
								Columns: []openapi.ConnectionColumn{
									{
										Name:     "id",
										Type:     "objectId",
										Nullable: false,
									},
								},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name:    "invalid json input",
			input:   `{invalid json`,
			want:    openapi.ConnectionSchemaResponse{},
			wantErr: true,
		},
		{
			name:    "empty array input",
			input:   `[]`,
			want:    openapi.ConnectionSchemaResponse{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMongoDBSchema(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseMongoDBSchema() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			assert.Equal(t, got, tt.want)
		})
	}
}

// TestParseMongoDBSchemaWithComplexIndexes tests the handling of complex indexes
func TestParseMongoDBSchemaWithComplexIndexes(t *testing.T) {
	input := `[
			{
					"schema_name": "testdb",
					"object_name": "users",
					"column_name": "email",
					"column_type": "string",
					"not_null": true
			},
			{
					"schema_name": "testdb",
					"object_name": "users",
					"column_name": "age",
					"column_type": "int",
					"not_null": false
			}
	]`

	want := openapi.ConnectionSchemaResponse{
		Schemas: []openapi.ConnectionSchema{
			{
				Name: "testdb",
				Tables: []openapi.ConnectionTable{
					{
						Name: "users",
						Columns: []openapi.ConnectionColumn{
							{
								Name:     "email",
								Type:     "string",
								Nullable: false,
							},
							{
								Name:     "age",
								Type:     "int",
								Nullable: true,
							},
						},
					},
				},
			},
		},
	}

	got, err := parseMongoDBSchema(input)
	if err != nil {
		t.Errorf("parseMongoDBSchema() error = %v", err)
		return
	}
	assert.Equal(t, got, want)
}

func TestParseSQLSchema(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		connType pb.ConnectionType
		want     openapi.ConnectionSchemaResponse
	}{
		{
			name: "postgres simple table",
			input: `schema_name	object_type	object_name	column_name	column_type	not_null
public	table	users	id	integer	t
public	table	users	email	varchar	f`,
			connType: pb.ConnectionTypePostgres,
			want: openapi.ConnectionSchemaResponse{
				Schemas: []openapi.ConnectionSchema{
					{
						Name: "public",
						Tables: []openapi.ConnectionTable{
							{
								Name: "users",
								Columns: []openapi.ConnectionColumn{
									{Name: "id", Type: "integer", Nullable: false},
									{Name: "email", Type: "varchar", Nullable: true},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "mysql multiple tables",
			input: `schema_name	object_type	object_name	column_name	column_type	not_null
app	table	users	id	int	1
app	table	users	name	varchar	0
app	table	products	id	int	1`,
			connType: pb.ConnectionTypeMySQL,
			want: openapi.ConnectionSchemaResponse{
				Schemas: []openapi.ConnectionSchema{
					{
						Name: "app",
						Tables: []openapi.ConnectionTable{
							{
								Name: "users",
								Columns: []openapi.ConnectionColumn{
									{Name: "id", Type: "int", Nullable: false},
									{Name: "name", Type: "varchar", Nullable: true},
								},
							},
							{
								Name: "products",
								Columns: []openapi.ConnectionColumn{
									{Name: "id", Type: "int", Nullable: false},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "mssql with header dashes",
			input: `schema_name	object_type	object_name	column_name	column_type	not_null
-----------	-----------	-----------	-----------	-----------	---------
dbo	table	customers	id	int	1
dbo	table	customers	name	varchar	0`,
			connType: pb.ConnectionTypeMSSQL,
			want: openapi.ConnectionSchemaResponse{
				Schemas: []openapi.ConnectionSchema{
					{
						Name: "dbo",
						Tables: []openapi.ConnectionTable{
							{
								Name: "customers",
								Columns: []openapi.ConnectionColumn{
									{Name: "id", Type: "int", Nullable: false},
									{Name: "name", Type: "varchar", Nullable: true},
								},
							},
						},
					},
				},
			},
		},
		{
			name:     "empty input",
			input:    "",
			connType: pb.ConnectionTypePostgres,
			want: openapi.ConnectionSchemaResponse{
				Schemas: []openapi.ConnectionSchema{},
			},
		},
		{
			name:     "invalid line format",
			input:    "schema_name	object_type	object_name	column_name", // menos campos que o necessário
			connType: pb.ConnectionTypePostgres,
			want: openapi.ConnectionSchemaResponse{
				Schemas: []openapi.ConnectionSchema{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSQLSchema(tt.input, tt.connType)
			if err != nil {
				t.Errorf("parseSQLSchema() error = %v", err)
				return
			}

			// Helper para comparar os resultados de forma mais detalhada
			compareSchemaResponses(t, got, tt.want)
		})
	}
}

// Helper function para comparar as respostas e dar mensagens de erro mais detalhadas
func compareSchemaResponses(t *testing.T, got, want openapi.ConnectionSchemaResponse) {
	if len(got.Schemas) != len(want.Schemas) {
		t.Errorf("schema count mismatch: got %d schemas, want %d schemas",
			len(got.Schemas), len(want.Schemas))
		return
	}

	for i, wantSchema := range want.Schemas {
		gotSchema := got.Schemas[i]
		if gotSchema.Name != wantSchema.Name {
			t.Errorf("schema[%d].Name: got %q, want %q",
				i, gotSchema.Name, wantSchema.Name)
			continue
		}

		if len(gotSchema.Tables) != len(wantSchema.Tables) {
			t.Errorf("schema[%d] (%s) table count mismatch: got %d tables, want %d tables",
				i, wantSchema.Name, len(gotSchema.Tables), len(wantSchema.Tables))
			continue
		}

		for j, wantTable := range wantSchema.Tables {
			gotTable := gotSchema.Tables[j]
			if gotTable.Name != wantTable.Name {
				t.Errorf("schema[%d].table[%d].Name: got %q, want %q",
					i, j, gotTable.Name, wantTable.Name)
				continue
			}

			if len(gotTable.Columns) != len(wantTable.Columns) {
				t.Errorf("schema[%d].table[%d] (%s.%s) column count mismatch: got %d columns, want %d columns",
					i, j, wantSchema.Name, wantTable.Name, len(gotTable.Columns), len(wantTable.Columns))
				continue
			}

			for k, wantColumn := range wantTable.Columns {
				gotColumn := gotTable.Columns[k]
				assert.Equal(t, gotColumn, wantColumn)
			}
		}
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
			name:    "starts with number",
			dbName:  "1database",
			wantErr: true,
		},
		{
			name:    "reserved word postgres",
			dbName:  "postgres",
			wantErr: true,
		},
		{
			name:    "reserved word master",
			dbName:  "master",
			wantErr: true,
		},
		{
			name:    "reserved word information_schema",
			dbName:  "information_schema",
			wantErr: true,
		},
		{
			name:    "unicode characters",
			dbName:  "database💾",
			wantErr: true,
		},
		{
			name:    "case insensitive reserved word",
			dbName:  "MASTER",
			wantErr: true,
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
