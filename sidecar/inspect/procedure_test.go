package inspect_test

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// ProceduresOf reads the name the codec recorded before it reads the text:
// a TDS RPCRequest names its procedure outside the SQL, and the text is a
// rendering. Without metadata the lexer reads every top-level EXEC or CALL.
func TestProceduresOf(t *testing.T) {
	for _, tc := range []struct {
		name string
		stmt inspect.Statement
		want string
	}{
		{"rpc metadata wins", inspect.Statement{
			Protocol: inspect.MSSQL, Direction: inspect.FromClient,
			Text: "EXEC get_secrets", Operation: inspect.OpUnknown,
			Metadata: map[string]string{inspect.MetadataMSSQLProc: "[dbo].[get_secrets]"},
		}, "[dbo].[get_secrets]"},
		{"batch text", inspect.Statement{
			Protocol: inspect.MSSQL, Direction: inspect.FromClient,
			Text: "exec dbo.get_secrets", Operation: inspect.OpUnknown,
		}, "dbo.get_secrets"},
		{"batch with a call behind SET", inspect.Statement{
			Protocol: inspect.MSSQL, Direction: inspect.FromClient,
			Text: "SET NOCOUNT ON; EXEC dbo.get_secrets; EXEC dbo.audit_touch", Operation: inspect.OpUnknown,
		}, "dbo.get_secrets,dbo.audit_touch"},
		{"postgres call", inspect.Statement{
			Protocol: inspect.Postgres, Direction: inspect.FromClient,
			Text: "CALL purge()", Operation: inspect.OpUnknown,
		}, "purge"},
		{"mysql call", inspect.Statement{
			Protocol: inspect.MySQL, Direction: inspect.FromClient,
			Text: "CALL app.purge(1)", Operation: inspect.OpUnknown,
		}, "app.purge"},
		{"brackets need the mssql dialect", inspect.Statement{
			Protocol: inspect.Postgres, Direction: inspect.FromClient,
			Text: "EXEC [dbo].[x]", Operation: inspect.OpUnknown,
		}, ""},
		{"a response calls nothing", inspect.Statement{
			Protocol: inspect.MSSQL, Direction: inspect.FromServer,
			Text: "EXEC dbo.get_secrets", Operation: inspect.OpUnknown,
		}, ""},
		{"not a call", inspect.Statement{
			Protocol: inspect.Postgres, Direction: inspect.FromClient,
			Text: "SELECT * FROM procs", Operation: inspect.OpSelect,
		}, ""},
		{"no SQL on the lane", inspect.Statement{
			Protocol: inspect.SSH, Direction: inspect.FromClient,
			Text: "exec dbo.get_secrets", Operation: inspect.OpUnknown,
		}, ""},
	} {
		if got := strings.Join(inspect.ProceduresOf(tc.stmt), ","); got != tc.want {
			t.Errorf("%s: ProceduresOf = %q, want %q", tc.name, got, tc.want)
		}
	}
}
