package pgrest

import "os"

var postgresOrgs = map[string]any{}

func WithPostgres(ctx OrgContext) bool {
	if os.Getenv("ORG_MULTI_TENANT") == "true" {
		return false
	}
	if _, ok := postgresOrgs[ctx.GetOrgID()]; ok {
		return false
	}
	return true
}
