package apiconnections

import (
	"strings"
	"testing"

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
