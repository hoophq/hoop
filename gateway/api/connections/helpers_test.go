package apiconnections

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
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

// Helper functions
func findTable(tables []openapi.ConnectionTable, name string) *openapi.ConnectionTable {
	for i := range tables {
		if tables[i].Name == name {
			return &tables[i]
		}
	}
	return nil
}

func findColumn(columns []openapi.ConnectionColumn, name string) *openapi.ConnectionColumn {
	for i := range columns {
		if columns[i].Name == name {
			return &columns[i]
		}
	}
	return nil
}

func TestParseSQLSchema(t *testing.T) {
	testInput := `schema_name	object_type	object_name	column_name	column_type	not_null	column_default	is_primary_key	is_foreign_key	index_name	index_columns	index_is_unique	index_is_primary
public	table	categories	category	integer	t	nextval('categories_category_seq'::regclass)	f	f	categories_pkey	{category}	t	t
public	table	categories	categoryname	character varying(50)	t		f	f	categories_pkey	{category}	t	t
public	table	cust_hist	customerid	integer	t		f	t	ix_cust_hist_customerid	{customerid}	f	f
public	table	cust_hist	orderid	integer	t		f	f	ix_cust_hist_customerid	{customerid}	f	f
public	table	customers	customerid	integer	t	nextval('customers_customerid_seq'::regclass)	f	f	customers_pkey	{customerid}	t	t
public	table	customers	email	character varying(50)	f		f	f	ix_cust_username	{username}	t	f`

	result, err := parseSQLSchema(testInput, pb.ConnectionTypePostgres)
	assert.NoError(t, err)

	// Basic structure validation
	assert.Len(t, result.Schemas, 1)
	schema := result.Schemas[0]
	assert.Equal(t, "public", schema.Name)
	assert.Len(t, schema.Tables, 3)

	// Test categories table
	categoriesTable := findTable(schema.Tables, "categories")
	assert.NotNil(t, categoriesTable)
	assert.Len(t, categoriesTable.Columns, 2)
	assert.Len(t, categoriesTable.Indexes, 1)

	// Test categories columns
	catIdCol := findColumn(categoriesTable.Columns, "category")
	assert.NotNil(t, catIdCol)
	assert.Equal(t, "integer", catIdCol.Type)
	assert.Equal(t, "nextval('categories_category_seq'::regclass)", catIdCol.DefaultValue)
	assert.False(t, catIdCol.Nullable)

	catNameCol := findColumn(categoriesTable.Columns, "categoryname")
	assert.NotNil(t, catNameCol)
	assert.Equal(t, "character varying(50)", catNameCol.Type)
	assert.False(t, catNameCol.Nullable)
	assert.Empty(t, catNameCol.DefaultValue)

	// Test categories indexes
	assert.Equal(t, "categories_pkey", categoriesTable.Indexes[0].Name)
	assert.Equal(t, []string{"{category}"}, categoriesTable.Indexes[0].Columns)
	assert.True(t, categoriesTable.Indexes[0].IsUnique)
	assert.True(t, categoriesTable.Indexes[0].IsPrimary)

	// Test cust_hist table
	custHistTable := findTable(schema.Tables, "cust_hist")
	assert.NotNil(t, custHistTable)
	assert.Len(t, custHistTable.Columns, 2)

	// Test cust_hist foreign key
	custIdCol := findColumn(custHistTable.Columns, "customerid")
	assert.NotNil(t, custIdCol)
	assert.True(t, custIdCol.IsForeignKey)
	assert.False(t, custIdCol.IsPrimaryKey)

	// Test customers table with nullable column
	customersTable := findTable(schema.Tables, "customers")
	assert.NotNil(t, customersTable)
	emailCol := findColumn(customersTable.Columns, "email")
	assert.NotNil(t, emailCol)
	assert.True(t, emailCol.Nullable)

	// Test edge cases
	emptyInput := ""
	emptyResult, err := parseSQLSchema(emptyInput, pb.ConnectionTypePostgres)
	assert.NoError(t, err)
	assert.Len(t, emptyResult.Schemas, 0)

	invalidInput := "invalid\tformat\tdata"
	invalidResult, err := parseSQLSchema(invalidInput, pb.ConnectionTypePostgres)
	assert.NoError(t, err)
	assert.Len(t, invalidResult.Schemas, 0)
}

func TestOrganizeSchemaResponse(t *testing.T) {
	// Test case with multiple tables, views, columns and indexes
	input := []map[string]interface{}{
		{
			"schema_name":      "public",
			"object_type":      "table",
			"object_name":      "categories",
			"column_name":      "category",
			"column_type":      "integer",
			"not_null":         true,
			"column_default":   "nextval('categories_category_seq'::regclass)",
			"is_primary_key":   true,
			"is_foreign_key":   false,
			"index_name":       "categories_pkey",
			"index_columns":    []string{"category"},
			"index_is_unique":  true,
			"index_is_primary": true,
		},
		{
			"schema_name":      "public",
			"object_type":      "table",
			"object_name":      "categories",
			"column_name":      "categoryname",
			"column_type":      "character varying(50)",
			"not_null":         true,
			"column_default":   "",
			"is_primary_key":   false,
			"is_foreign_key":   false,
			"index_name":       "categories_pkey",
			"index_columns":    []string{"category"},
			"index_is_unique":  true,
			"index_is_primary": true,
		},
		{
			"schema_name":      "public",
			"object_type":      "view",
			"object_name":      "active_categories",
			"column_name":      "category",
			"column_type":      "integer",
			"not_null":         true,
			"column_default":   "",
			"is_primary_key":   false,
			"is_foreign_key":   false,
			"index_name":       "",
			"index_columns":    []string{},
			"index_is_unique":  false,
			"index_is_primary": false,
		},
	}

	result := organizeSchemaResponse(input)

	// Validate basic structure
	assert.Len(t, result.Schemas, 1)
	schema := result.Schemas[0]
	assert.Equal(t, "public", schema.Name)
	assert.Len(t, schema.Tables, 1)
	assert.Len(t, schema.Views, 1)

	// Validate table
	table := schema.Tables[0]
	assert.Equal(t, "categories", table.Name)
	assert.Len(t, table.Columns, 2)
	assert.Len(t, table.Indexes, 1)

	// Validate columns
	idCol := findColumn(table.Columns, "category")
	assert.NotNil(t, idCol)
	assert.Equal(t, "integer", idCol.Type)
	assert.Equal(t, "nextval('categories_category_seq'::regclass)", idCol.DefaultValue)
	assert.False(t, idCol.Nullable)
	assert.True(t, idCol.IsPrimaryKey)

	nameCol := findColumn(table.Columns, "categoryname")
	assert.NotNil(t, nameCol)
	assert.Equal(t, "character varying(50)", nameCol.Type)
	assert.False(t, nameCol.Nullable)
	assert.Empty(t, nameCol.DefaultValue)
	assert.False(t, nameCol.IsPrimaryKey)

	// Validate index
	assert.Equal(t, "categories_pkey", table.Indexes[0].Name)
	assert.Equal(t, []string{"category"}, table.Indexes[0].Columns)
	assert.True(t, table.Indexes[0].IsUnique)
	assert.True(t, table.Indexes[0].IsPrimary)

	// Validate view
	view := schema.Views[0]
	assert.Equal(t, "active_categories", view.Name)
	assert.Len(t, view.Columns, 1)

	viewCol := view.Columns[0]
	assert.Equal(t, "category", viewCol.Name)
	assert.Equal(t, "integer", viewCol.Type)
	assert.False(t, viewCol.Nullable)
	assert.Empty(t, viewCol.DefaultValue)
	assert.False(t, viewCol.IsPrimaryKey)

	// Test case with multiple schemas
	multiSchemaInput := []map[string]interface{}{
		{
			"schema_name":      "public",
			"object_type":      "table",
			"object_name":      "table1",
			"column_name":      "id",
			"column_type":      "integer",
			"not_null":         true,
			"column_default":   "",
			"is_primary_key":   true,
			"is_foreign_key":   false,
			"index_name":       "",
			"index_columns":    []string{},
			"index_is_unique":  false,
			"index_is_primary": false,
		},
		{
			"schema_name":      "app",
			"object_type":      "table",
			"object_name":      "table2",
			"column_name":      "id",
			"column_type":      "integer",
			"not_null":         true,
			"column_default":   "",
			"is_primary_key":   true,
			"is_foreign_key":   false,
			"index_name":       "",
			"index_columns":    []string{},
			"index_is_unique":  false,
			"index_is_primary": false,
		},
	}

	multiResult := organizeSchemaResponse(multiSchemaInput)
	assert.Len(t, multiResult.Schemas, 2)
	assert.ElementsMatch(t, []string{"public", "app"}, []string{
		multiResult.Schemas[0].Name,
		multiResult.Schemas[1].Name,
	})

	// Test case with empty input
	emptyResult := organizeSchemaResponse([]map[string]interface{}{})
	assert.Len(t, emptyResult.Schemas, 0)

	// Test case with multiple columns in same table
	sameTableInput := []map[string]interface{}{
		{
			"schema_name":      "public",
			"object_type":      "table",
			"object_name":      "users",
			"column_name":      "id",
			"column_type":      "integer",
			"not_null":         true,
			"column_default":   "",
			"is_primary_key":   true,
			"is_foreign_key":   false,
			"index_name":       "",
			"index_columns":    []string{},
			"index_is_unique":  false,
			"index_is_primary": false,
		},
		{
			"schema_name":      "public",
			"object_type":      "table",
			"object_name":      "users",
			"column_name":      "email",
			"column_type":      "varchar",
			"not_null":         true,
			"column_default":   "",
			"is_primary_key":   false,
			"is_foreign_key":   false,
			"index_name":       "",
			"index_columns":    []string{},
			"index_is_unique":  false,
			"index_is_primary": false,
		},
	}

	sameTableResult := organizeSchemaResponse(sameTableInput)
	assert.Len(t, sameTableResult.Schemas, 1)
	assert.Len(t, sameTableResult.Schemas[0].Tables, 1)
	assert.Len(t, sameTableResult.Schemas[0].Tables[0].Columns, 2)
}

// Test helper function to validate the output format matches the real DB output
func TestParseSQLSchemaWithRealOutput(t *testing.T) {
	input := `schema_name	object_type	object_name	column_name	column_type	not_null	column_default	is_primary_key	is_foreign_key	index_name	index_columns	index_is_unique	index_is_primary
public	table	categories	category	integer	t	nextval('categories_category_seq'::regclass)	f	f	categories_pkey	{category}	t	t
public	table	categories	categoryname	character varying(50)	t		f	f	categories_pkey	{category}	t	t
public	table	customers	customerid	integer	t	nextval('customers_customerid_seq'::regclass)	f	f	customers_pkey	{customerid}	t	t
public	table	customers	firstname	character varying(50)	t		f	f	customers_pkey	{customerid}	t	t
public	table	customers	email	character varying(50)	f		f	f	customers_pkey	{customerid}	t	t`

	got, err := parseSQLSchema(input, pb.ConnectionTypePostgres)
	assert.NoError(t, err)

	// Validate basic structure
	assert.Len(t, got.Schemas, 1)
	assert.Equal(t, "public", got.Schemas[0].Name)

	// Validate tables
	assert.Len(t, got.Schemas[0].Tables, 2)

	// Check categories table
	categoriesTable := got.Schemas[0].Tables[0]
	assert.Equal(t, "categories", categoriesTable.Name)
	assert.Len(t, categoriesTable.Columns, 2)
	assert.Equal(t, "category", categoriesTable.Columns[0].Name)
	assert.Equal(t, "integer", categoriesTable.Columns[0].Type)
	assert.Equal(t, "categoryname", categoriesTable.Columns[1].Name)

	// Check customers table
	customersTable := got.Schemas[0].Tables[1]
	assert.Equal(t, "customers", customersTable.Name)
	assert.Len(t, customersTable.Columns, 3)
	assert.Equal(t, "customerid", customersTable.Columns[0].Name)
	assert.Equal(t, "firstname", customersTable.Columns[1].Name)
	assert.Equal(t, "email", customersTable.Columns[2].Name)
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
