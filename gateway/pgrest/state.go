package pgrest

import "os"

var postgresOrgs = map[string]any{
	"3aea97dd-2a8f-426b-9d0e-dd278ad19f6c": nil,
	"<org-id2>":                            nil,
}

func WithPostgres(ctx OrgContext) bool {
	if os.Getenv("ORG_MULTI_TENANT") == "true" {
		return false
	}
	_, ok := postgresOrgs[ctx.GetOrgID()]
	return ok
}
