package policy

import (
	"fmt"
	"strings"

	"github.com/hoophq/hoop/sidecar/inspect"
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
		for _, part := range parts {
			if part == "" {
				return nil, fmt.Errorf("%s: procedure %q has an empty part", r.Name, p)
			}
		}
		out = append(out, parts)
	}
	return out, nil
}

// matchesProcedure evaluates the procedure rule type. ok reports whether
// this rule type belongs here, mirroring matchesMetadata. A response calls
// nothing, so it never matches, whatever its text says.
func (r Rule) matchesProcedure(stmt inspect.Statement) (matched, ok bool) {
	if r.Type != MatchProcedure {
		return false, false
	}
	if stmt.Direction == inspect.FromServer {
		return false, true
	}
	called := procedureParts(inspect.ProcedureOf(stmt))
	if len(called) == 0 {
		return false, true
	}
	for _, want := range r.procedures {
		if trailingParts(called, want) {
			return true, true
		}
	}
	return false, true
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
// (`]]`, `""`, two backticks) resolved to one byte, each part then trimmed
// and lowercased. A dot inside a delimiter stays in its part, so
// `[dbo.sys].x` is two parts. An empty name gives nil.
func procedureParts(name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	var (
		parts []string
		part  strings.Builder
		close byte // the delimiter a part is inside, or 0
	)
	flush := func() {
		parts = append(parts, strings.ToLower(strings.TrimSpace(part.String())))
		part.Reset()
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
			close = ']'
		case c == '"', c == '`':
			close = c
		case c == '.':
			flush()
		default:
			part.WriteByte(c)
		}
	}
	flush()
	return parts
}

// procedureValue is what a deferring procedure rule reports: the normalized
// name the rule matched, so a policy reads one spelling whatever the client
// sent.
func procedureValue(stmt inspect.Statement) string {
	return strings.Join(procedureParts(inspect.ProcedureOf(stmt)), ".")
}
