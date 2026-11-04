package policy

import (
	"fmt"
	"slices"
	"strings"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// MatchMCPTool denies an MCP tools/call by tool name: a call to a tool Tools
// names, or to any tool Allow does not. An mcp lane also hides from tools/list
// every tool the rule denies.
const MatchMCPTool MatchType = "mcp_tool"

type mcpRuleFields struct {
	// Tools for MatchMCPTool: globs of the tools the rule denies.
	Tools []string `json:"tools,omitempty" cap:"rule_mcp_tool"`

	// Allow for MatchMCPTool: globs of the only tools the rule lets through.
	Allow []string `json:"allow,omitempty" cap:"rule_mcp_tool"`
}

func (r Rule) validateMCPTool() error {
	if (len(r.Tools) == 0) == (len(r.Allow) == 0) {
		return fmt.Errorf("%s: an mcp_tool rule sets tools or allow, and not both", r.Name)
	}
	for _, g := range slices.Concat(r.Tools, r.Allow) {
		if strings.TrimSpace(g) == "" {
			return fmt.Errorf("%s: an mcp_tool rule with an empty glob", r.Name)
		}
		if strings.TrimSpace(g) != g {
			return fmt.Errorf("%s: the mcp_tool glob %q has spaces around it", r.Name, g)
		}
	}
	return nil
}

// matchesMCPTool evaluates the mcp_tool rule type. ok reports whether this
// rule type belongs here, mirroring matchesHTTP.
func (r Rule) matchesMCPTool(stmt inspect.Statement) (matched, ok bool) {
	if r.Type != MatchMCPTool {
		return false, false
	}
	tool, named := stmt.Metadata[inspect.MetadataMCPTool]
	if stmt.Protocol != inspect.MCP || stmt.Operation != inspect.OpCall || !named {
		return false, true
	}
	matches := func(g string) bool { return toolGlob(g, tool) }
	if len(r.Tools) > 0 {
		return slices.ContainsFunc(r.Tools, matches), true
	}
	return !slices.ContainsFunc(r.Allow, matches), true
}

// toolGlob matches name against pattern. `*` matches any run of characters;
// every other byte matches itself, case included, as MCP tool names are
// case-sensitive.
func toolGlob(pattern, name string) bool {
	p, n, star, mark := 0, 0, -1, 0
	for n < len(name) {
		switch {
		case p < len(pattern) && pattern[p] == '*':
			star, mark = p, n
			p++
		case p < len(pattern) && pattern[p] == name[n]:
			p++
			n++
		case star >= 0:
			mark++
			p, n = star+1, mark
		default:
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}
