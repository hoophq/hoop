package apiconnections

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
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
										Name:         "id",
										Type:         "objectId",
										Nullable:     false,
										DefaultValue: "",
										IsPrimaryKey: true,
										IsForeignKey: false,
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
										Name:         "email",
										Type:         "string",
										Nullable:     false,
										DefaultValue: "",
										IsPrimaryKey: false,
										IsForeignKey: false,
									},
								},
								Indexes: []openapi.ConnectionIndex{
									{
										Name:      "email_idx",
										Columns:   []string{"email"},
										IsUnique:  true,
										IsPrimary: false,
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
										Name:         "id",
										Type:         "objectId",
										Nullable:     false,
										IsPrimaryKey: true,
										IsForeignKey: false,
									},
									{
										Name:         "name",
										Type:         "string",
										Nullable:     true,
										IsPrimaryKey: false,
										IsForeignKey: false,
									},
								},
							},
							{
								Name: "posts",
								Columns: []openapi.ConnectionColumn{
									{
										Name:         "id",
										Type:         "objectId",
										Nullable:     false,
										IsPrimaryKey: true,
										IsForeignKey: false,
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
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseMongoDBSchema() = %v, want %v", got, tt.want)
			}
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
					"not_null": true,
					"index_name": "compound_idx",
					"index_columns": "email,age,name",
					"index_is_unique": true,
					"index_is_primary": false
			},
			{
					"schema_name": "testdb",
					"object_name": "users",
					"column_name": "age",
					"column_type": "int",
					"not_null": false,
					"index_name": "compound_idx",
					"index_columns": "email,age,name",
					"index_is_unique": true,
					"index_is_primary": false
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
						Indexes: []openapi.ConnectionIndex{
							{
								Name:      "compound_idx",
								Columns:   []string{"email", "age", "name"},
								IsUnique:  true,
								IsPrimary: false,
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
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseMongoDBSchema() = %v, want %v", got, want)
	}
}

func TestParseSQLSchema(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		connType pb.ConnectionType
		want     openapi.ConnectionSchemaResponse
		wantErr  bool
	}{
		{
			name: "postgres schema",
			input: `public	table	users	id	integer	0	NULL	1	0	pk_users	id	1	1
public	table	users	name	varchar	0	NULL	0	0	NULL	NULL	0	0
public	table	users	email	varchar	0	NULL	0	0	idx_email	email	1	0`,
			connType: pb.ConnectionTypePostgres,
			want: openapi.ConnectionSchemaResponse{
				Schemas: []openapi.ConnectionSchema{
					{
						Name: "public",
						Tables: []openapi.ConnectionTable{
							{
								Name: "users",
								Columns: []openapi.ConnectionColumn{
									{
										Name:         "id",
										Type:         "integer",
										Nullable:     true,
										DefaultValue: "NULL",
										IsPrimaryKey: true,
										IsForeignKey: false,
									},
									{
										Name:         "name",
										Type:         "varchar",
										Nullable:     true,
										DefaultValue: "NULL",
										IsPrimaryKey: false,
										IsForeignKey: false,
									},
									{
										Name:         "email",
										Type:         "varchar",
										Nullable:     true,
										DefaultValue: "NULL",
										IsPrimaryKey: false,
										IsForeignKey: false,
									},
								},
								Indexes: []openapi.ConnectionIndex{
									{
										Name:      "pk_users",
										Columns:   []string{"id"},
										IsUnique:  true,
										IsPrimary: true,
									},
									{
										Name:      "idx_email",
										Columns:   []string{"email"},
										IsUnique:  true,
										IsPrimary: false,
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "mssql schema with header",
			input: `schema_name	object_type	object_name	column_name	column_type	not_null	column_default	is_primary_key	is_foreign_key	index_name	index_columns	index_is_unique	index_is_primary
-----------	-----------	-----------	-----------	-----------	--------	-------------	--------------	--------------	----------	-------------	---------------	----------------
dbo	table	customers	id	int	0	NULL	1	0	PK_customers	id	1	1
dbo	table	customers	name	nvarchar	0	NULL	0	0	NULL	NULL	0	0`,
			connType: pb.ConnectionTypeMSSQL,
			want: openapi.ConnectionSchemaResponse{
				Schemas: []openapi.ConnectionSchema{
					{
						Name: "dbo",
						Tables: []openapi.ConnectionTable{
							{
								Name: "customers",
								Columns: []openapi.ConnectionColumn{
									{
										Name:         "id",
										Type:         "int",
										Nullable:     true,
										DefaultValue: "NULL",
										IsPrimaryKey: true,
										IsForeignKey: false,
									},
									{
										Name:         "name",
										Type:         "nvarchar",
										Nullable:     true,
										DefaultValue: "NULL",
										IsPrimaryKey: false,
										IsForeignKey: false,
									},
								},
								Indexes: []openapi.ConnectionIndex{
									{
										Name:      "PK_customers",
										Columns:   []string{"id"},
										IsUnique:  true,
										IsPrimary: true,
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "mysql schema with foreign key",
			input: `test	table	orders	user_id	int	0	NULL	0	1	fk_user	user_id	0	0
test	table	orders	order_id	int	0	NULL	1	0	pk_orders	order_id	1	1`,
			connType: pb.ConnectionTypeMySQL,
			want: openapi.ConnectionSchemaResponse{
				Schemas: []openapi.ConnectionSchema{
					{
						Name: "test",
						Tables: []openapi.ConnectionTable{
							{
								Name: "orders",
								Columns: []openapi.ConnectionColumn{
									{
										Name:         "user_id",
										Type:         "int",
										Nullable:     true,
										DefaultValue: "NULL",
										IsPrimaryKey: false,
										IsForeignKey: true,
									},
									{
										Name:         "order_id",
										Type:         "int",
										Nullable:     true,
										DefaultValue: "NULL",
										IsPrimaryKey: true,
										IsForeignKey: false,
									},
								},
								Indexes: []openapi.ConnectionIndex{
									{
										Name:      "fk_user",
										Columns:   []string{"user_id"},
										IsUnique:  false,
										IsPrimary: false,
									},
									{
										Name:      "pk_orders",
										Columns:   []string{"order_id"},
										IsUnique:  true,
										IsPrimary: true,
									},
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
			want:     openapi.ConnectionSchemaResponse{},
		},
		{
			name:     "invalid format",
			input:    "invalid\tformat",
			connType: pb.ConnectionTypePostgres,
			want:     openapi.ConnectionSchemaResponse{},
		},
		{
			name:     "only footer",
			input:    "(3 rows affected)",
			connType: pb.ConnectionTypePostgres,
			want:     openapi.ConnectionSchemaResponse{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSQLSchema(tt.input, tt.connType)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseSQLSchema() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseSQLSchema() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestParseSQLSchemaCompoundIndexes tests handling of compound indexes
func TestParseSQLSchemaCompoundIndexes(t *testing.T) {
	input := `public	table	users	id	integer	0	NULL	1	0	pk_users	id	1	1
public	table	users	email	varchar	0	NULL	0	0	idx_email_name	email,name	1	0
public	table	users	name	varchar	0	NULL	0	0	idx_email_name	email,name	1	0`

	want := openapi.ConnectionSchemaResponse{
		Schemas: []openapi.ConnectionSchema{
			{
				Name: "public",
				Tables: []openapi.ConnectionTable{
					{
						Name: "users",
						Columns: []openapi.ConnectionColumn{
							{
								Name:         "id",
								Type:         "integer",
								Nullable:     true,
								DefaultValue: "NULL",
								IsPrimaryKey: true,
								IsForeignKey: false,
							},
							{
								Name:         "email",
								Type:         "varchar",
								Nullable:     true,
								DefaultValue: "NULL",
								IsPrimaryKey: false,
								IsForeignKey: false,
							},
							{
								Name:         "name",
								Type:         "varchar",
								Nullable:     true,
								DefaultValue: "NULL",
								IsPrimaryKey: false,
								IsForeignKey: false,
							},
						},
						Indexes: []openapi.ConnectionIndex{
							{
								Name:      "pk_users",
								Columns:   []string{"id"},
								IsUnique:  true,
								IsPrimary: true,
							},
							{
								Name:      "idx_email_name",
								Columns:   []string{"email", "name"},
								IsUnique:  true,
								IsPrimary: false,
							},
						},
					},
				},
			},
		},
	}

	got, err := parseSQLSchema(input, pb.ConnectionTypePostgres)
	if err != nil {
		t.Errorf("parseSQLSchema() error = %v", err)
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseSQLSchema() = %v, want %v", got, want)
	}
}
