package analyzer_test

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

// The plane's routing agent reads the shape of a held statement. No name and
// no email in it survives, wherever the statement puts them.
func TestAStatementShapeHoldsNoValue(t *testing.T) {
	secrets := []string{"ana", "souza", "reilly", "alice", "example", "4111"}
	for _, tc := range []struct {
		protocol, text, want string
	}{
		{"postgres", "UPDATE users SET name = 'Ana Souza', email = \"ana.souza@corp.example\" WHERE id = 4111",
			"update users set name = ?, email = ? where id = ?"},
		{"postgres", "DELETE FROM users -- asked by Ana Souza\nWHERE email = $tag$ana.souza@corp.example$tag$ /* ana /* souza */ 4111 */",
			"delete from users where email = ?"},
		{"postgres", "SELECT * FROM users WHERE email = E'ana\\'s ana.souza@corp.example' AND id = $1",
			"select * from users where email = ? and id = $ ?"},
		{"mysql", "SELECT * FROM `users` WHERE name = 'Ana Souza' # ana.souza@corp.example\nAND id = 4111",
			"select * from ? where name = ? and id = ?"},
		{"mysql", "SELECT * FROM t WHERE name = \"Ana Souza\" /*! AND 4111 */", "select * from t where name = ? and ?"},
		{"mssql", "UPDATE users SET name = N'Ana O''Souza' WHERE email = 'ana.souza@corp.example' -- Ana",
			"update users set name = ? where email = ?"},
		{"oracle", "SELECT q'[O'Reilly alice@example.com]', q'{Ana's}', q'(Ana's)', q'<Ana's>', " +
			"q'!Ana's alice@example.com!', nq'[O'Reilly]', NQ'{Ana's}' FROM dual",
			"select ?, ?, ?, ?, ?, ?, ? from dual"},
		{"oracle", "DELETE FROM users WHERE name = :name AND id = 4111 -- Ana Souza", "delete from users where name = ? and id = ?"},
		{"clickhouse", "SELECT 1 # alice@example.com", "select ?"},
		{"clickhouse", "SELECT * FROM users WHERE email = 'alice@example.com\\' 4111' -- Ana", "select * from users where email = ?"},
		{"mongodb", `{"delete":"users","deletes":[{"q":{"name":"Ana Souza","email":"ana.souza@corp.example"},"limit":4111}]}`, ""},
	} {
		got, ok := analyzer.StatementShape(tc.protocol, tc.text)
		if !ok {
			t.Errorf("%s: no shape for %q", tc.protocol, tc.text)
			continue
		}
		for _, s := range secrets {
			if strings.Contains(strings.ToLower(got), s) {
				t.Errorf("%s: shape %q keeps %q", tc.protocol, got, s)
			}
		}
		if tc.want != "" && got != tc.want {
			t.Errorf("%s: shape = %q, want %q", tc.protocol, got, tc.want)
		}
	}
}

// No shape where none can be made without values: a protocol with no SQL
// or JSON, a mongodb command that does not decode, a statement the lexer
// cannot read in full, and MySQL text whose backslash readings disagree.
func TestAStatementShapeIsRefusedWhereItCannotStrip(t *testing.T) {
	for _, tc := range []struct{ protocol, text string }{
		{"http", "POST /users name=Ana"},
		{"ssh", "cat /home/ana"},
		{"grpc", `{"name":"Ana"}`},
		{"mongodb", `{"find":"users","filter":{"name":"Ana"`},
		{"postgres", "SELECT 'Ana Souza"},
		{"oracle", "SELECT q' Ana Souza ' FROM dual"},
		{"oracle", "SELECT q'[Ana Souza' FROM dual"},
		{"mysql", "SELECT * FROM `users WHERE name = 'Ana Souza'"},
		{"mysql", "SELECT * FROM t WHERE name = 'Ana\\' OR alice@example.com = '' -- '"},
	} {
		if got, ok := analyzer.StatementShape(tc.protocol, tc.text); ok || got != "" {
			t.Errorf("%s %q: shape = %q, %t", tc.protocol, tc.text, got, ok)
		}
	}
}
