package policy_test

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// call is a stored procedure call as the mssql codec reports a TDS
// RPCRequest: the rendered text, operation unknown with the reason, and the
// name as the client sent it in the metadata.
func call(proc string) inspect.Statement {
	return inspect.Statement{
		Protocol: inspect.MSSQL, Direction: inspect.FromClient,
		Text: "EXEC " + proc, Operation: inspect.OpUnknown,
		Metadata: map[string]string{
			inspect.MetadataMSSQLProc:     proc,
			inspect.MetadataSQLIncomplete: "stored procedure; body is in the catalog",
		},
	}
}

// batch is the same call in a SQL batch: text only, no metadata.
func batch(proto inspect.Protocol, text string) inspect.Statement {
	return inspect.Statement{
		Protocol: proto, Direction: inspect.FromClient,
		Text: text, Operation: inspect.OpUnknown,
	}
}

func procedureRule(procs ...string) *policy.Rules {
	rules, err := policy.NewRules([]policy.Rule{{
		Name: "no-secrets", Type: policy.MatchProcedure, Procedures: procs,
	}})
	if err != nil {
		panic(err)
	}
	return rules
}

// A configured name matches the trailing parts of the called one, whatever
// the client's spelling: delimiters and case are the client's choice, and a
// bare name matches any qualification, as a table rule's does.
func TestProcedureMatchesAcrossSpellings(t *testing.T) {
	for _, tc := range []struct {
		rule, called string
		want         bool
	}{
		{"get_secrets", "dbo.get_secrets", true},
		{"get_secrets", "[dbo].[get_secrets]", true},
		{"get_secrets", "appdb.dbo.get_secrets", true},
		{"get_secrets", "GET_SECRETS", true},
		{"get_secrets", `"dbo"."Get_Secrets"`, true},
		{"dbo.get_secrets", "appdb.dbo.get_secrets", true},
		{"dbo.get_secrets", "[dbo].[get_secrets]", true},
		{"[dbo].[get_secrets]", "dbo.get_secrets", true},
		{"dbo.get_secrets", "custom.get_secrets", false},
		{"appdb.dbo.get_secrets", "dbo.get_secrets", false},
		{"get_secrets", "dbo.get_customers", false},
		{"get_secrets", "dbo.get_secrets_v2", false},
		// A dot inside a delimiter is part of the name, not a separator.
		{"x", "[dbo.sys].x", true},
		{"dbo.sys.x", "[dbo.sys].x", false},
		{"[dbo.sys].x", "[dbo.sys].x", true},
		{"sys.x", "[dbo.sys].x", false},
		// ]] inside brackets is one ].
		{"x]", "[x]]]", true},
		{"[x]]]", "x]", true},
		{"x", "[x]]]", false},
	} {
		got := procedureRule(tc.rule).Evaluate(call(tc.called)).Denied
		if got != tc.want {
			t.Errorf("procedures: [%s] on %s: denied=%v, want %v", tc.rule, tc.called, got, tc.want)
		}
	}
}

// A batch names the procedure in its text only; the lexer reads every
// top-level call there, under the lane's dialect. `SET NOCOUNT ON; EXEC p`
// runs p, so it matches: sqlcmd and most drivers prefix a batch with SET.
func TestProcedureMatchesABatchByItsText(t *testing.T) {
	rules := procedureRule("get_secrets", "purge")
	for _, tc := range []struct {
		stmt inspect.Statement
		want bool
	}{
		{batch(inspect.MSSQL, "exec dbo.get_secrets"), true},
		{batch(inspect.MSSQL, "EXECUTE [dbo].[get_secrets] @id = 1"), true},
		{batch(inspect.MSSQL, "EXEC dbo.get_customers"), false},
		{batch(inspect.Postgres, "CALL purge()"), true},
		{batch(inspect.MySQL, "CALL `app`.`purge`(1)"), true},
		{batch(inspect.Postgres, "SELECT purge()"), false},
		{batch(inspect.MSSQL, "SET NOCOUNT ON; EXEC dbo.get_secrets"), true},
		{batch(inspect.MSSQL, "EXEC dbo.get_customers; EXEC dbo.get_secrets"), true},
	} {
		if got := rules.Evaluate(tc.stmt).Denied; got != tc.want {
			t.Errorf("%s: denied=%v, want %v", tc.stmt.Text, got, tc.want)
		}
	}
}

// The acceptance case: one rule, both shapes of the same call.
func TestProcedureDeniesTheRPCAndTheBatch(t *testing.T) {
	rules := procedureRule("get_secrets")
	rpc := call("[dbo].[get_secrets]")
	rpc.Text = "EXEC dbo.get_secrets"
	if v := rules.Evaluate(rpc); !v.Denied || v.Rule != "no-secrets" || v.Source != "procedure" {
		t.Errorf("rpc call: %+v, want denied by no-secrets", v)
	}
	if !rules.Evaluate(batch(inspect.MSSQL, "exec dbo.get_secrets")).Denied {
		t.Error("the batch form was allowed")
	}
	if rules.Evaluate(batch(inspect.MSSQL, "EXEC dbo.get_customers")).Denied {
		t.Error("another procedure was denied")
	}
}

// A response calls nothing. The codecs report one as unknown too, and a rule
// that matched it would refuse every result on the lane.
func TestProcedureNeverMatchesAResponse(t *testing.T) {
	rules := procedureRule("get_secrets")
	resp := call("dbo.get_secrets")
	resp.Direction = inspect.FromServer
	if rules.Evaluate(resp).Denied {
		t.Error("a server statement carrying the procedure's name was denied")
	}
}

// The operations scope narrows a procedure rule as it narrows every other
// type: a rule scoped to `call` never sees the unknown a stored procedure is.
func TestProcedureHonoursTheOperationsScope(t *testing.T) {
	scoped := func(ops ...inspect.Operation) *policy.Rules {
		rules, err := policy.NewRules([]policy.Rule{{
			Name: "r", Type: policy.MatchProcedure, Procedures: []string{"get_secrets"},
			Operations: ops,
		}})
		if err != nil {
			t.Fatal(err)
		}
		return rules
	}
	if !scoped(inspect.OpUnknown).Evaluate(call("dbo.get_secrets")).Denied {
		t.Error("scoped to unknown, the rule did not match an unknown call")
	}
	if scoped(inspect.OpCall).Evaluate(call("dbo.get_secrets")).Denied {
		t.Error("scoped to call, the rule matched an unknown statement")
	}
}

// A deferring procedure rule reports the normalized name, so a policy
// compares against one spelling whatever the client sent.
func TestProcedureDeferReportsTheNormalizedName(t *testing.T) {
	rules, err := policy.NewRules([]policy.Rule{{
		Name: "watch-secrets", Type: policy.MatchProcedure,
		Procedures: []string{"get_secrets"}, Action: policy.ActionDefer,
	}})
	if err != nil {
		t.Fatal(err)
	}
	var ec policy.EvalContext
	if v := rules.EvaluateWith(call("[AppDB].[dbo].[Get_Secrets]"), &ec); v.Denied {
		t.Fatalf("a deferring rule denied: %+v", v)
	}
	f, ok := ec.Finding(string(policy.MatchProcedure))
	if !ok {
		t.Fatalf("the match was not reported: %v", ec.Findings)
	}
	if f.Rule != "watch-secrets" || f.Status != policy.FindingOK {
		t.Errorf("finding = %+v", f)
	}
	if got := f.Values["procedure"]; got != "appdb.dbo.get_secrets" {
		t.Errorf("values.procedure = %v, want the normalized name", got)
	}
}

// A rule naming no procedure would load and match nothing, the failure this
// package refuses everywhere; so would a name that normalizes to nothing.
func TestProcedureRuleWithoutProceduresIsRefused(t *testing.T) {
	for name, procs := range map[string][]string{
		"empty list":  nil,
		"empty name":  {""},
		"only dots":   {"."},
		"empty part":  {"dbo."},
		"empty first": {".x"},
	} {
		_, err := policy.NewRules([]policy.Rule{{Name: "r", Type: policy.MatchProcedure, Procedures: procs}})
		if err == nil {
			t.Errorf("%s: NewRules accepted the rule", name)
		}
	}
}

// A user refused for an unknown statement is told why the scanner gave up,
// because `operation=unknown` alone does not say what to change. The
// operator's own message is never rewritten.
func TestUnknownDenialMessageCarriesTheReason(t *testing.T) {
	unknown, err := policy.NewRules([]policy.Rule{{
		Name: "no-unknown", Type: policy.MatchOperation,
		Operations: []inspect.Operation{inspect.OpUnknown},
	}})
	if err != nil {
		t.Fatal(err)
	}
	v := unknown.Evaluate(call("dbo.get_secrets"))
	if !v.Denied || !strings.Contains(v.Message, "stored procedure; body is in the catalog") {
		t.Errorf("message = %q, want the sql.incomplete reason", v.Message)
	}

	strict, err := policy.NewRules([]policy.Rule{{
		Name: "fence", Type: policy.MatchTable,
		Tables: []string{"customers"}, RequireTableMatch: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	v = strict.Evaluate(call("dbo.get_secrets"))
	if !v.Denied || !strings.Contains(v.Message, "stored procedure; body is in the catalog") {
		t.Errorf("require_table_match message = %q, want the sql.incomplete reason", v.Message)
	}

	own, err := policy.NewRules([]policy.Rule{{
		Name: "no-unknown", Type: policy.MatchOperation,
		Operations: []inspect.Operation{inspect.OpUnknown}, Message: "ask the DBA",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if v := own.Evaluate(call("dbo.get_secrets")); v.Message != "ask the DBA" {
		t.Errorf("message = %q, want the operator's", v.Message)
	}

	// A known operation keeps the short form.
	if v := strict.Evaluate(stmt("DELETE FROM customers", inspect.OpDelete, "customers")); !v.Denied ||
		strings.Contains(v.Message, "unknown") {
		t.Errorf("message = %q", v.Message)
	}
}
