package policy_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

func mcpCall(tool string) inspect.Statement {
	return inspect.Statement{
		Protocol:  inspect.MCP,
		Direction: inspect.FromClient,
		Operation: inspect.OpCall,
		Text:      tool + "\n{}",
		Tables:    []string{strings.ToLower(tool)},
		Metadata:  map[string]string{inspect.MetadataMCPMethod: "tools/call", inspect.MetadataMCPTool: tool},
	}
}

// mcpRules builds rules from the JSON a config file holds.
func mcpRules(t *testing.T, doc string) (*policy.Rules, error) {
	t.Helper()
	var rules []policy.Rule
	if err := json.Unmarshal([]byte(doc), &rules); err != nil {
		t.Fatal(err)
	}
	return policy.NewRules(rules)
}

// An mcp_tool rule denies a call by tool name: tools lists the denied ones,
// allow the only ones let through. Globs take `*` only and case counts.
func TestMCPToolRule(t *testing.T) {
	for _, c := range []struct {
		name, rule string
		deny, pass []string
	}{
		{
			name: "tools",
			rule: `[{"name":"r","type":"mcp_tool","tools":["*delete*","run_query"]}]`,
			deny: []string{"delete_customer", "bulk_delete_rows", "run_query"},
			pass: []string{"echo", "Delete_customer", "run_query_v2"},
		},
		{
			name: "allow",
			rule: `[{"name":"r","type":"mcp_tool","allow":["get_*","list_*"]}]`,
			deny: []string{"delete_customer", "Get_file", "search"},
			pass: []string{"get_file_contents", "list_issues", "get_"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			rules, err := mcpRules(t, c.rule)
			if err != nil {
				t.Fatal(err)
			}
			for _, tool := range c.deny {
				if v := rules.Evaluate(mcpCall(tool)); !v.Denied || v.Rule != "r" {
					t.Errorf("%s: %+v, want denied by r", tool, v)
				}
			}
			for _, tool := range c.pass {
				if v := rules.Evaluate(mcpCall(tool)); v.Denied {
					t.Errorf("%s: denied, want allowed", tool)
				}
			}
		})
	}
}

// The rule reads only an MCP tools/call: the catalog statement, another
// protocol, a statement without a tool name and a SQL statement never match, so a top-level
// rule every lane inherits stays silent outside mcp lanes.
func TestMCPToolRuleReadsOnlyMCPCalls(t *testing.T) {
	rules, err := mcpRules(t, `[{"name":"r","type":"mcp_tool","allow":["echo"]}]`)
	if err != nil {
		t.Fatal(err)
	}
	catalog := inspect.Statement{Protocol: inspect.MCP, Direction: inspect.FromServer, Operation: inspect.OpShow, Text: "delete_customer: Delete a customer.\n"}
	unnamed := mcpCall("x")
	delete(unnamed.Metadata, inspect.MetadataMCPTool)
	sql := mcpCall("execute_sql")
	sql.Metadata[inspect.MetadataMCPSQLIndex] = "1"
	for name, s := range map[string]inspect.Statement{
		"catalog":       catalog,
		"postgres":      stmt("SELECT 1", inspect.OpSelect, "delete_customer"),
		"unnamed":       unnamed,
		"sql statement": sql,
	} {
		if rules.Evaluate(s).Denied {
			t.Errorf("%s: denied", name)
		}
	}
}

func TestMCPToolRuleValidation(t *testing.T) {
	for _, c := range []struct{ name, rule, want string }{
		{"neither", `[{"name":"r","type":"mcp_tool"}]`, "tools or allow"},
		{"both", `[{"name":"r","type":"mcp_tool","tools":["a"],"allow":["b"]}]`, "tools or allow"},
		{"empty glob", `[{"name":"r","type":"mcp_tool","tools":[" "]}]`, "empty glob"},
		{"spaces", `[{"name":"r","type":"mcp_tool","tools":[" delete_*"]}]`, "spaces around it"},
	} {
		if _, err := mcpRules(t, c.rule); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}
