package lexer_test

import (
	"testing"

	"github.com/hoophq/hoop/sidecar/lexer"
)

// A statement that opens with EXEC, EXECUTE or CALL names its procedure in
// Callee, as the client wrote it. The spelling is kept whole because a
// policy normalizes one form, and dropping a delimiter here would make
// `[dbo].[x]` and `dbo.x` two names.
func TestCalleeOfALeadingCall(t *testing.T) {
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
		// Not the leading verb.
		{`SET NOCOUNT ON; EXEC dbo.get_secrets`, lexer.MSSQL, ""},
		{`SELECT * FROM t`, lexer.Postgres, ""},
		{`BEGIN pkg.purge(); END;`, lexer.Oracle, ""},
		{`EXPLAIN CALL purge()`, lexer.Postgres, ""},
	} {
		a := lexer.Analyze(tc.sql, tc.d)
		if a.Callee != tc.want {
			t.Errorf("Callee = %q, want %q: %s", a.Callee, tc.want, tc.sql)
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
