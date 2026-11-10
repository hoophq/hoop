package daemon

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/jsonsql"
	"github.com/hoophq/hoop/sidecar/lexer"
	"github.com/hoophq/hoop/sidecar/policy"
)

// MCPSQLTool names the tools/call argument that holds SQL, and its dialect.
type MCPSQLTool struct {
	Argument string `json:"argument,omitempty"`
	Dialect  string `json:"dialect,omitempty"`
}

// mcpSQLRule names a refusal of a SQL argument the lane cannot read.
const mcpSQLRule = "mcp_gateway.sql"

func validateMCPSQL(name string, tools map[string]MCPSQLTool) []string {
	var problems []string
	globs := make([]string, 0, len(tools))
	for g := range tools {
		globs = append(globs, g)
	}
	sort.Strings(globs)
	for _, g := range globs {
		t := tools[g]
		key := fmt.Sprintf("mcp_gateway.sql[%q]", g)
		if strings.TrimSpace(g) == "" || strings.TrimSpace(g) != g {
			problems = append(problems, fmt.Sprintf("%s: %s is not a tool glob; write it as an mcp_tool rule does, with no spaces around it", name, key))
		}
		if strings.TrimSpace(t.Argument) == "" || strings.TrimSpace(t.Argument) != t.Argument {
			problems = append(problems, fmt.Sprintf("%s: %s.argument %q is not an argument name; name the top-level argument that holds the SQL", name, key, t.Argument))
		}
		if p := sqlDialectProblem(key+".dialect", t.Dialect); p != "" {
			problems = append(problems, name+": "+p)
		}
	}
	return problems
}

// mcpSQLFor returns the entry of tools whose glob matches tool. The longest
// glob wins; two of that length that name different entries are an error.
func mcpSQLFor(tools map[string]MCPSQLTool, tool string) (MCPSQLTool, bool, error) {
	var hits []string
	longest := -1
	for g := range tools {
		if policy.ToolGlob(g, tool) {
			hits = append(hits, g)
			longest = max(longest, len(g))
		}
	}
	hits = slices.DeleteFunc(hits, func(g string) bool { return len(g) < longest })
	if len(hits) == 0 {
		return MCPSQLTool{}, false, nil
	}
	sort.Strings(hits)
	for _, g := range hits[1:] {
		if tools[g] != tools[hits[0]] {
			return MCPSQLTool{}, false, fmt.Errorf("mcp_gateway.sql globs %q and %q both match tool %q and disagree", hits[0], g, tool)
		}
	}
	return tools[hits[0]], true, nil
}

// sqlStatements reads t's argument from args and splits it into statements.
func (t MCPSQLTool) sqlStatements(args []byte) ([]string, error) {
	sql, err := jsonsql.Member(args, t.Argument)
	if err != nil {
		return nil, fmt.Errorf("arguments: %w", err)
	}
	parts := lexer.Split(sql, httpSQLDialects[t.Dialect])
	if len(parts) == 0 {
		return nil, fmt.Errorf("arguments: member %q holds no SQL statement", t.Argument)
	}
	return parts, nil
}

// sqlCall is a call to a SQL tool: the call statement, then one statement
// per SQL statement of its argument.
func (s *mcpLaneSession) sqlCall(ctx context.Context, call inspect.Statement, t MCPSQLTool, args []byte) MCPVerdict {
	parts, err := t.sqlStatements(args)
	if err != nil {
		return s.refuseSQL(ctx, call.Metadata[inspect.MetadataMCPTool], t.Argument, err)
	}
	call.Metadata[inspect.MetadataMCPSQLStatements] = strconv.Itoa(len(parts))
	stmts := []inspect.Statement{call}
	d := httpSQLDialects[t.Dialect]
	for i, sql := range parts {
		a := inspect.AnalyzeSQLIn(sql, d)
		st := inspect.Statement{
			Protocol:  inspect.MCP,
			Direction: inspect.FromClient,
			Text:      sql,
			Operation: a.Operation,
			Effects:   a.Effects,
			Relations: a.Relations,
			Tables:    a.Tables,
			Metadata: map[string]string{
				inspect.MetadataMCPMethod:      call.Metadata[inspect.MetadataMCPMethod],
				inspect.MetadataMCPTool:        call.Metadata[inspect.MetadataMCPTool],
				inspect.MetadataMCPSession:     call.Metadata[inspect.MetadataMCPSession],
				inspect.MetadataMCPSQLArgument: t.Argument,
				inspect.MetadataMCPSQLDialect:  t.Dialect,
				inspect.MetadataMCPSQLIndex:    strconv.Itoa(i + 1),
			},
		}
		if !a.Complete {
			st.Metadata[inspect.MetadataSQLIncomplete] = a.Reason
		}
		stmts = append(stmts, st)
	}
	return s.evaluate(ctx, stmts...)
}

// refuseSQL refuses a call whose SQL argument the lane cannot read, before
// any rule: no rule can judge SQL it cannot see.
func (s *mcpLaneSession) refuseSQL(ctx context.Context, tool, argument string, reason error) MCPVerdict {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return MCPVerdict{Message: "session closed"}
	}
	if err := s.g.RecordActivity(ctx, "mcp_sql_unreadable", map[string]string{"tool": tool, "argument": argument, "reason": reason.Error()}); err != nil {
		s.log.Warn("mcp activity not recorded", "activity", "mcp_sql_unreadable", "error", err)
	}
	return MCPVerdict{Rule: mcpSQLRule, Message: "the SQL argument cannot be read: " + reason.Error()}
}
