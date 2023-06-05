package dcm

import "time"

const (
	policyConfigKeyName = "policy-config"
	maxPolicyInstances  = 10
)

// https://www.postgresql.org/docs/14/sql-grant.html
var (
	allowedGrantPrivileges = map[string]any{
		"SELECT":     nil,
		"INSERT":     nil,
		"UPDATE":     nil,
		"DELETE":     nil,
		"TRUNCATE":   nil,
		"REFERENCES": nil,
		"TRIGGER":    nil,
		"CREATE":     nil,
		"TEMPORARY":  nil,
	}
	defaultExpirationDuration = time.Hour * 12
)
