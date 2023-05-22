package dcm

// the configuration key for privileges, the privileges
// must be separated by semicolon. E.g.: SELECT;UPDATE;...
const privilegeConfigNameKey = "PRIVILEGES"

// https://www.postgresql.org/docs/14/sql-grant.html
var grantPrivileges = map[string]any{
	"SELECT":     nil,
	"INSERT":     nil,
	"UPDATE":     nil,
	"DELETE":     nil,
	"TRUNCATE":   nil,
	"REFERENCES": nil,
	"TRIGGER":    nil,
	"CREATE":     nil,
	"CONNECT":    nil,
	"TEMPORARY":  nil,
	"EXECUTE":    nil,
	"USAGE":      nil,
}
