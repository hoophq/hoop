package yaml_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/config/yaml"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// One rule block, shared by a YAML anchor, denies on a postgres lane and on
// an mcp lane: a table rule names a table on one door and a tool on the other.
func TestOneAnchoredRuleBlockDeniesOnAPostgresAndAnMCPLane(t *testing.T) {
	cfg, err := yaml.LoadYAMLBytes([]byte(`
x-shared-rules: &shared
  - {name: no-customer-deletes, type: table, tables: [customers, delete_customer], message: customers are not deleted here}

listeners:
  - name: appdb
    protocol: postgres
    listen: 127.0.0.1:15432
    upstream: db:5432
    guardrails: {rules: *shared}
  - name: tools
    protocol: mcp
    listen: 127.0.0.1:18901
    upstream: https://mcp.example.com/mcp
    guardrails: {rules: *shared}
`))
	if err != nil {
		t.Fatal(err)
	}
	pg, mcp := cfg.Listeners[0].Guardrails.Rules, cfg.Listeners[1].Guardrails.Rules
	if len(pg) != 1 || !reflect.DeepEqual(pg, mcp) {
		t.Fatalf("the anchor did not give both lanes one block: %+v, %+v", pg, mcp)
	}

	sql := func(text string) inspect.Statement {
		a := inspect.AnalyzeSQL(text, inspect.Postgres)
		return inspect.Statement{Protocol: inspect.Postgres, Direction: inspect.FromClient, Text: text,
			Operation: a.Operation, Effects: a.Effects, Relations: a.Relations, Tables: a.Tables}
	}
	call := func(tool string) inspect.Statement {
		return inspect.Statement{Protocol: inspect.MCP, Direction: inspect.FromClient, Text: tool + "\n{}",
			Operation: inspect.OpCall, Tables: []string{strings.ToLower(tool)},
			Metadata: map[string]string{inspect.MetadataMCPMethod: "tools/call", inspect.MetadataMCPTool: tool}}
	}
	for i, c := range []struct {
		lane   int
		stmt   inspect.Statement
		denied bool
	}{
		{0, sql("DELETE FROM customers WHERE id = 7"), true},
		{0, sql("SELECT 1"), false},
		{1, call("delete_customer"), true},
		{1, call("echo"), false},
	} {
		rules, err := policy.NewRules(cfg.Listeners[c.lane].Guardrails.Rules)
		if err != nil {
			t.Fatal(err)
		}
		if v := rules.Evaluate(c.stmt); v.Denied != c.denied || (v.Denied && v.Rule != "no-customer-deletes") {
			t.Errorf("case %d on %s: %q denied=%v rule=%q, want denied=%v", i, cfg.Listeners[c.lane].Name, c.stmt.Text, v.Denied, v.Rule, c.denied)
		}
	}
}
