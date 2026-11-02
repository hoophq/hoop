package lexer_test

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/lexer"
)

// Every top-level EXEC, EXECUTE or CALL names its procedure in Callees, as
// the client wrote it. The spelling is kept whole because a policy
// normalizes one form, and dropping a delimiter here would make `[dbo].[x]`
// and `dbo.x` two names. `SET NOCOUNT ON; EXEC p` runs p, so a call behind
// another statement counts; a call inside a block or under EXPLAIN does not.
func TestCalleesOfTopLevelCalls(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		d    lexer.Dialect
		want string
	}{
		{`EXEC dbo.get_secrets`, lexer.MSSQL, "dbo.get_secrets"},
		{`exec dbo.get_secrets;`, lexer.MSSQL, "dbo.get_secrets"},
		{`EXECUTE [dbo].[get_secrets] @id = 1`, lexer.MSSQL, "[dbo].[get_secrets]"},
		{`EXEC [my proc]`, lexer.MSSQL, "[my proc]"},
		{`EXEC [x]]]`, lexer.MSSQL, "[x]]]"},
		{`EXEC appdb.dbo.Get_Secrets 1, 'a'`, lexer.MSSQL, "appdb.dbo.Get_Secrets"},
		{`EXEC @status = dbo.get_secrets`, lexer.MSSQL, "dbo.get_secrets"},
		{`EXEC sp_executesql N'DROP TABLE t'`, lexer.MSSQL, "sp_executesql"},
		{`/* c */ exec dbo.get_secrets -- x`, lexer.MSSQL, "dbo.get_secrets"},
		{`CALL purge()`, lexer.Postgres, "purge"},
		{`CALL "Ops".purge(1)`, lexer.Postgres, `"Ops".purge`},
		{"CALL `app`.`purge`()", lexer.MySQL, "`app`.`purge`"},
		{`CALL pkg.purge()`, lexer.Oracle, "pkg.purge"},

		// Not a procedure.
		{`EXEC ('DROP TABLE t')`, lexer.MSSQL, ""},
		{`EXECUTE IMMEDIATE 'DROP TABLE t'`, lexer.Oracle, ""},
		{`EXEC`, lexer.MSSQL, ""},
		// Behind another statement, still top level: it runs.
		{`SET NOCOUNT ON; EXEC dbo.get_secrets`, lexer.MSSQL, "dbo.get_secrets"},
		{`EXEC dbo.a; EXEC dbo.b`, lexer.MSSQL, "dbo.a,dbo.b"},
		{`SELECT * FROM t`, lexer.Postgres, ""},
		{`BEGIN pkg.purge(); END;`, lexer.Oracle, ""},
		{`EXPLAIN CALL purge()`, lexer.Postgres, ""},
	} {
		a := lexer.Analyze(tc.sql, tc.d)
		if got := strings.Join(a.Callees, ","); got != tc.want {
			t.Errorf("Callees = %q, want %q: %s", got, tc.want, tc.sql)
		}
	}
}

// Callee adds nothing to the verb or the completeness: a call stays an
// unreadable call.
func TestCalleeLeavesTheCallUnreadable(t *testing.T) {
	a := lexer.Analyze(`EXEC dbo.get_secrets`, lexer.MSSQL)
	if a.Verb != lexer.Call || a.Complete || a.Reason == "" {
		t.Errorf("Analyze = %+v, want an incomplete call with a reason", a)
	}
}
