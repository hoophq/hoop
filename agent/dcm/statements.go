package dcm

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

const (
	createRoleStatementTmpl      = `CREATE ROLE "{{.user}}" WITH LOGIN ENCRYPTED PASSWORD '{{.password}}' VALID UNTIL '{{.valid_until}}'`
	grantPrivilegesStatementTmpl = `GRANT {{.privileges}} ON ALL TABLES IN SCHEMA {{.schema}} TO {{.user}}`
)

func parseCreateRoleStatementTmpl(user, passwd, validUntil string) (string, error) {
	return parseTemplate(createRoleStatementTmpl, map[string]string{
		"user":        user,
		"password":    passwd,
		"valid_until": validUntil,
	})
}

func parseGrantPrivilegesStatementTmpl(dbUser, schema string, privileges []string) (string, error) {
	return parseTemplate(grantPrivilegesStatementTmpl, map[string]string{
		"user":       dbUser,
		"schema":     schema,
		"privileges": strings.Join(privileges, ", "),
	})
}

func parseTemplate(tmpl string, data map[string]string) (string, error) {
	t, err := template.New("").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("failed parsing template, err=%v", err)
	}
	out := bytes.NewBufferString("")
	err = t.Execute(out, data)
	if err != nil {
		return "", fmt.Errorf("failed executing template, err=%v", err)
	}
	return out.String(), nil
}
