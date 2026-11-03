package policy

import (
	"fmt"
	"strings"
)

// A stored procedure call reaches policy as `EXEC <name>` with operation
// `unknown`: the body is in the catalog, so the scanner cannot say what it
// does. Before this type an operator fenced one procedure with a regex or a
// word list scoped to `operations: [unknown]`, and every spelling of the name
// (`dbo.x`, `[dbo].[x]`, `appdb.dbo.x`) needed its own pattern. This type
// reads the name itself, from the RPC metadata or the text, and compares one
// normalized form.
const (
	// MatchProcedure matches a client statement that calls one of
	// Procedures. Names compare case-insensitively after the delimiters
	// are removed, and a configured name matches the TRAILING parts of the
	// called one: `get_secrets` matches `dbo.get_secrets` and
	// `appdb.dbo.get_secrets`; `dbo.get_secrets` matches
	// `appdb.dbo.get_secrets` and not `custom.get_secrets`. A bare name
	// matches any schema qualification, as a `table` rule's does.
	MatchProcedure MatchType = "procedure"
)

// compileProcedures normalizes the rule's Procedures once, at load. A name
// that normalizes to nothing, or to an empty part, would match nothing or
// every call with an empty part, so it is refused with the others.
func (r Rule) compileProcedures() ([][]string, error) {
	if len(r.Procedures) == 0 {
		return nil, fmt.Errorf("%s: procedure rule with no procedures", r.Name)
	}
	out := make([][]string, 0, len(r.Procedures))
	for _, p := range r.Procedures {
		parts := procedureParts(p)
		if len(parts) == 0 {
			return nil, fmt.Errorf("%s: procedure rule names an empty procedure", r.Name)
		}
		for i, part := range parts {
			// SQL Server lets a three-part name omit its schema:
			// `master..sp_executesql`. Any other empty part names nothing.
			if part == "" && !(len(parts) == 3 && i == 1) {
				return nil, fmt.Errorf("%s: procedure %q has an empty part", r.Name, p)
			}
		}
		out = append(out, parts)
	}
	return out, nil
}

// matchesProcedure evaluates the procedure rule type against the
// procedures the statement calls, which Rules reads once per statement
// (inspect.ProceduresOf runs the lexer) and hands to every procedure rule.
// A batch runs every call it names, so a rule naming any one of them has a
// say. A response calls nothing; the caller passes nil for one.
func (r Rule) matchesProcedure(called []string) bool {
	for _, name := range called {
		parts := procedureParts(name)
		if len(parts) == 0 {
			continue
		}
		for _, want := range r.procedures {
			if trailingParts(parts, want) {
				return true
			}
		}
	}
	return false
}

// trailingParts reports whether want equals the last len(want) parts of got.
func trailingParts(got, want []string) bool {
	if len(want) > len(got) {
		return false
	}
	got = got[len(got)-len(want):]
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// procedureParts normalizes a procedure name as the client or the operator
// wrote it: parts split on `.` outside delimiters, with the bracket, double
// quote and backtick delimiters removed and a doubled close inside one
// (`]]`, `""`, two backticks) resolved to one byte, each part lowercased.
// Whitespace is trimmed around a part, never inside a delimiter: SQL Server
// keeps the spaces in `[ secret ]`, which names a different procedure from
// `secret`. A dot inside a delimiter stays in its part, so `[dbo.sys].x` is
// two parts. An empty name gives nil.
func procedureParts(name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	var (
		parts     []string
		part      strings.Builder
		close     byte // the delimiter a part is inside, or 0
		delimited bool // the part being built came from a delimiter
	)
	flush := func() {
		p := part.String()
		if !delimited {
			p = strings.TrimSpace(p)
		}
		parts = append(parts, strings.ToLower(p))
		part.Reset()
		delimited = false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case close != 0:
			if c != close {
				part.WriteByte(c)
				continue
			}
			if i+1 < len(name) && name[i+1] == close {
				part.WriteByte(c)
				i++
				continue
			}
			close = 0
		case c == '[':
			close, delimited = ']', true
		case c == '"', c == '`':
			close, delimited = c, true
		case c == '.':
			flush()
		case delimited && (c == ' ' || c == '\t'):
			// Whitespace between a closed delimiter and the next dot.
		default:
			part.WriteByte(c)
		}
	}
	flush()
	return parts
}

// procedureValue is what a deferring procedure rule reports: the normalized
// name of the first configured procedure the statement calls, so a policy
// reads one spelling whatever the client sent. A statement calling several
// is reported by the one the rule was written for; the others are in the
// statement text.
func (r Rule) procedureValue(called []string) string {
	for _, name := range called {
		parts := procedureParts(name)
		for _, want := range r.procedures {
			if trailingParts(parts, want) {
				return strings.Join(parts, ".")
			}
		}
	}
	return ""
}
