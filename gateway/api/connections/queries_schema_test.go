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

func TestGetTablesQueryRejectsUnsafeIdentifiers(t *testing.T) {
	for _, connType := range []pb.ConnectionType{
		pb.ConnectionTypePostgres,
		pb.ConnectionTypeMSSQL,
		pb.ConnectionTypeMySQL,
		pb.ConnectionTypeMongoDB,
	} {
		for _, dbName := range []string{
			"db`; DROP TABLE users; --",
			"db'; DROP TABLE users; --",
			"db name",
			"",
		} {
			script, err := getTablesQuery(connType, dbName)
			assert.Error(t, err, "connType=%s dbName=%q", connType, dbName)
			assert.Empty(t, script, "connType=%s dbName=%q", connType, dbName)
		}
	}
}

func TestGetColumnsQueryRejectsUnsafeIdentifiers(t *testing.T) {
	for _, connType := range []pb.ConnectionType{
		pb.ConnectionTypePostgres,
		pb.ConnectionTypeMSSQL,
		pb.ConnectionTypeMySQL,
		pb.ConnectionTypeMongoDB,
		pb.ConnectionTypeOracleDB,
		pb.ConnectionTypeDynamoDB,
	} {
		for _, tt := range []struct {
			msg                           string
			dbName, tableName, schemaName string
		}{
			{"backtick breakout in database", "db` USE mysql; --", "users", "db"},
			{"quote breakout in table", "db", "users' OR '1'='1", "db"},
			{"quote breakout in schema", "db", "users", "db' UNION SELECT authentication_string FROM mysql.user; --"},
		} {
			// Only assert on identifiers each type actually interpolates:
			// DynamoDB uses only the table, OracleDB uses table+schema, and
			// MongoDB uses database+table.
			switch connType {
			case pb.ConnectionTypeDynamoDB:
				if tt.tableName == "users" {
					continue
				}
			case pb.ConnectionTypeOracleDB:
				if tt.tableName == "users" && tt.schemaName == "db" {
					continue
				}
			case pb.ConnectionTypeMongoDB:
				if tt.dbName == "db" && tt.tableName == "users" {
					continue
				}
			}
			script, err := getColumnsQuery(connType, tt.dbName, tt.tableName, tt.schemaName)
			assert.Error(t, err, "connType=%s case=%s", connType, tt.msg)
			assert.Empty(t, script, "connType=%s case=%s", connType, tt.msg)
		}
	}
}

func TestGetColumnsQueryAcceptsValidIdentifiers(t *testing.T) {
	for _, connType := range []pb.ConnectionType{
		pb.ConnectionTypePostgres,
		pb.ConnectionTypeMSSQL,
		pb.ConnectionTypeMySQL,
		pb.ConnectionTypeMongoDB,
		pb.ConnectionTypeOracleDB,
		pb.ConnectionTypeDynamoDB,
	} {
		script, err := getColumnsQuery(connType, "db-prod", "users", "db-prod")
		assert.NoError(t, err, "connType=%s", connType)
		assert.NotEmpty(t, script, "connType=%s", connType)
	}
}
