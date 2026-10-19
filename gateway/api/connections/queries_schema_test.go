package apiconnections

import (
	"strings"
	"testing"

	pb "github.com/hoophq/hoop/common/proto"
	"github.com/stretchr/testify/assert"
)

func TestGetMySQLColumnsQuery(t *testing.T) {
	for _, tt := range []struct {
		msg     string
		dbName  string
		wantUse string
	}{
		{
			msg:     "it must quote database names with hyphens",
			dbName:  "name-prod",
			wantUse: "USE `name-prod`;",
		},
		{
			msg:     "it must quote database names with dots",
			dbName:  "name.prod",
			wantUse: "USE `name.prod`;",
		},
		{
			msg:     "it must quote plain database names",
			dbName:  "name_prod",
			wantUse: "USE `name_prod`;",
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			query := getMySQLColumnsQuery(tt.dbName, "users", tt.dbName)
			assert.Contains(t, query, tt.wantUse)
			assert.Contains(t, query, "c.TABLE_SCHEMA = '"+tt.dbName+"'")
			assert.Contains(t, query, "c.TABLE_NAME = 'users'")
			// the identifier must never appear as a bare (unquoted) identifier
			assert.NotContains(t, query, "USE "+tt.dbName+";")
		})
	}
}

func TestGetMySQLColumnsQueryFormatIsComplete(t *testing.T) {
	// A mismatched fmt.Sprintf leaks error markers into the output instead of
	// failing at compile time; guard against that class of regression.
	query := getMySQLColumnsQuery("db-prod", "users", "db-prod")
	assert.False(t, strings.Contains(query, "%!"), "query contains fmt error markers: %s", query)
	assert.False(t, strings.Contains(query, "%s"), "query contains unexpanded verbs: %s", query)
}

func TestGetColumnsQueryQuotesNames(t *testing.T) {
	const table, schema = "it's", "o'reilly"
	for _, tt := range []struct {
		connType pb.ConnectionType
		want     []string
	}{
		{pb.ConnectionTypePostgres, []string{"c.relname = 'it''s'", "n.nspname = 'o''reilly'"}},
		{pb.ConnectionTypeMSSQL, []string{"s.name = 'o''reilly' AND o.name = 'it''s'"}},
		{pb.ConnectionTypeMySQL, []string{"c.TABLE_SCHEMA = 'o''reilly' AND c.TABLE_NAME = 'it''s'"}},
		{pb.ConnectionTypeOracleDB, []string{"c.owner = 'o''reilly' AND c.table_name = 'it''s'"}},
		{pb.ConnectionTypeMongoDB, []string{`var dbName = "db";`, `var collName = "it's";`}},
	} {
		t.Run(string(tt.connType), func(t *testing.T) {
			query, err := getColumnsQuery(tt.connType, "db", table, schema)
			assert.NoError(t, err)
			for _, want := range tt.want {
				assert.Contains(t, query, want)
			}
		})
	}
}

func TestGetColumnsQueryRejectsUnsafeNames(t *testing.T) {
	sqlTypes := []pb.ConnectionType{pb.ConnectionTypePostgres, pb.ConnectionTypeMSSQL,
		pb.ConnectionTypeMySQL, pb.ConnectionTypeOracleDB, pb.ConnectionTypeMongoDB}
	for _, connType := range sqlTypes {
		for _, name := range []string{"users\nDROP TABLE users", "users\rGO", `users\'`, "users\x00", "users\xbf'"} {
			_, err := getColumnsQuery(connType, "db", name, "public")
			assert.Error(t, err, "%s: table %q", connType, name)
			_, err = getColumnsQuery(connType, "db", "users", name)
			assert.Error(t, err, "%s: schema %q", connType, name)
		}
	}
	for _, connType := range []pb.ConnectionType{pb.ConnectionTypePostgres, pb.ConnectionTypeMSSQL,
		pb.ConnectionTypeMySQL, pb.ConnectionTypeMongoDB} {
		_, err := getColumnsQuery(connType, "db';--", "users", "public")
		assert.Error(t, err, "%s: database name", connType)
	}
	for _, name := range []string{"users; id", "users$(id)", "ab"} {
		_, err := getColumnsQuery(pb.ConnectionTypeDynamoDB, "", name, "")
		assert.Error(t, err, "dynamodb: table %q", name)
	}
	query, err := getColumnsQuery(pb.ConnectionTypeDynamoDB, "", "orders-2024.v1", "")
	assert.NoError(t, err)
	assert.Equal(t, "aws dynamodb describe-table --table-name orders-2024.v1 --output json", query)
}

func TestGetTablesQueryValidatesDatabaseName(t *testing.T) {
	for _, connType := range []pb.ConnectionType{pb.ConnectionTypePostgres, pb.ConnectionTypeMSSQL,
		pb.ConnectionTypeMySQL, pb.ConnectionTypeMongoDB} {
		_, err := getTablesQuery(connType, "db\n\\! id")
		assert.Error(t, err, connType)
		query, err := getTablesQuery(connType, "db-prod")
		assert.NoError(t, err, connType)
		assert.Contains(t, query, "db-prod")
	}
	// These types do not take a database name.
	for _, connType := range []pb.ConnectionType{pb.ConnectionTypeOracleDB, pb.ConnectionTypeDynamoDB, pb.ConnectionTypeCloudWatch} {
		query, err := getTablesQuery(connType, "")
		assert.NoError(t, err, connType)
		assert.NotEmpty(t, query, connType)
	}
	query, err := getTablesQuery(pb.ConnectionType("unknown"), "db")
	assert.NoError(t, err)
	assert.Empty(t, query)
}

// sqlplus would prompt for &B and read the rest of the script as its value.
func TestGetOracleColumnsQueryDisablesSubstitution(t *testing.T) {
	query, err := getColumnsQuery(pb.ConnectionTypeOracleDB, "", "A&B", "APP")
	assert.NoError(t, err)
	assert.True(t, strings.HasPrefix(strings.TrimSpace(query), "SET DEFINE OFF;"), query)
	assert.Contains(t, query, "c.table_name = 'A&B'")
}
