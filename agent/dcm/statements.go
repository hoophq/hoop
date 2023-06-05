package dcm

import (
	"fmt"
	"strings"
)

const (
	createRoleStatementTmpl          = `CREATE ROLE "%s" WITH LOGIN ENCRYPTED PASSWORD '%s' VALID UNTIL '%s'`
	alterRoleStatement               = `ALTER ROLE "%v" WITH LOGIN ENCRYPTED PASSWORD '%v' VALID UNTIL '%s'`
	revokeAllPrivilegesStatementTmpl = `REVOKE ALL ON ALL TABLES IN SCHEMA %s FROM %s`
	grantUsagePrivilegeStatementTmpl = `GRANT USAGE ON SCHEMA %s TO %s`
	grantPrivilegesStatementTmpl     = `GRANT %s ON ALL TABLES IN SCHEMA %s TO %s`
)

func createRoleStmt(user, passwd, validUntil string) string {
	return fmt.Sprintf(createRoleStatementTmpl, user, passwd, validUntil)
}

func alterRoleStmt(user, passwd, validUntil string) string {
	return fmt.Sprintf(alterRoleStatement, user, passwd, validUntil)
}

func grantPrivilegesStmt(dbUser, schema string, privileges []string) []string {
	grantPrivileges := strings.Join(privileges, ", ")
	return []string{
		fmt.Sprintf(revokeAllPrivilegesStatementTmpl, schema, dbUser),
		fmt.Sprintf(grantUsagePrivilegeStatementTmpl, schema, dbUser),
		fmt.Sprintf(grantPrivilegesStatementTmpl, grantPrivileges, schema, dbUser),
	}
}
