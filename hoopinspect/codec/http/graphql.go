package http

import (
	"encoding/json"
	"strings"

	"github.com/hoophq/hoopinspect"
)

// ParseGraphQL extracts the operation type, name, root fields and selection
// depth from a GraphQL request body.
//
// # Why this is the point of the package
//
// Every GraphQL request is `POST /graphql`. At the ext_authz layer that is
// one indistinguishable shape, so a method-and-path policy can only allow all
// GraphQL or none. The distinction between
//
//	query   { user(id: 1) { name } }        // a read
//	mutation { deleteUser(id: 1) }          // destroys a record
//
// exists only in the body. This function surfaces it, so a policy can say
// "engineering may query, only oncall may mutate, nobody may call
// deleteUser".
//
// # Scope
//
// This is a structural scanner, not a spec-complete GraphQL parser. It
// resolves what a policy needs — operation type, operation name, top-level
// selections, nesting depth — and deliberately does not validate the document
// against a schema. Returns nil when the body is not a recognizable GraphQL
// request, so a caller can treat nil as "not GraphQL" rather than "empty
// GraphQL".
//
// Fragments are not expanded: a root field reached only through a fragment
// spread is not listed. Callers writing deny rules on RootFields should pair
// them with an OperationType rule, which cannot be evaded that way.
func ParseGraphQL(body []byte) *hoopinspect.GraphQLDetail {
	query, opName := graphQLEnvelope(body)
	if query == "" {
		return nil
	}

	d := &hoopinspect.GraphQLDetail{OperationName: opName}
	toks := tokenizeGraphQL(query)
	if len(toks) == 0 {
		return nil
	}

	opType, cursor := graphQLOperationType(toks)
	d.OperationType = opType

	// A shorthand document (`{ user { name } }`) is a query with no name.
	if d.OperationName == "" {
		d.OperationName = graphQLOperationName(toks, cursor)
	}

	d.RootFields = graphQLRootFields(toks, cursor)
	d.Depth = graphQLDepth(toks)

	return d
}

// graphQLEnvelope pulls the query document and operation name out of the
// standard JSON envelope: {"query": "...", "operationName": "...",
// "variables": {...}}.
//
// A raw body with Content-Type application/graphql is also accepted: if the
// bytes are not JSON but look like a GraphQL document, they are the query.
func graphQLEnvelope(body []byte) (query, operationName string) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return "", ""
	}

	if trimmed[0] == '{' || trimmed[0] == '[' {
		// Batched requests ([{...},{...}]) are rejected rather than
		// half-inspected: reporting only the first operation would let the
		// rest through unexamined, which is worse than reporting nothing.
		if trimmed[0] == '[' {
			return "", ""
		}
		var env struct {
			Query         string `json:"query"`
			OperationName string `json:"operationName"`
		}
		if err := json.Unmarshal([]byte(trimmed), &env); err == nil && env.Query != "" {
			return env.Query, env.OperationName
		}
		// A JSON object without a `query` field is not a GraphQL request.
		// It may still be a bare document starting with `{` (shorthand), so
		// fall through only when it did not parse as JSON at all.
		if json.Valid([]byte(trimmed)) {
			return "", ""
		}
	}

	// Raw document body.
	if looksLikeGraphQL(trimmed) {
		return trimmed, ""
	}
	return "", ""
}

func looksLikeGraphQL(s string) bool {
	if !strings.Contains(s, "{") {
		return false
	}
	lead := strings.TrimSpace(s)
	for _, kw := range []string{"query", "mutation", "subscription", "fragment", "{"} {
		if strings.HasPrefix(lead, kw) {
			return true
		}
	}
	return false
}

// graphQLToken is a lexed token. Only the shapes a policy needs are
// distinguished.
type graphQLToken struct {
	text  string
	punct bool // true for { } ( ) : , ... etc
}

// tokenizeGraphQL lexes a document, discarding comments, string literals and
// commas (which GraphQL treats as whitespace).
//
// String literals are dropped so a field name appearing inside an argument
// value — `search(term: "deleteUser")` — cannot be mistaken for a selection.
func tokenizeGraphQL(s string) []graphQLToken {
	var toks []graphQLToken
	var cur strings.Builder

	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, graphQLToken{text: cur.String()})
			cur.Reset()
		}
	}

	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '#': // comment to end of line
			flush()
			for i < len(s) && s[i] != '\n' {
				i++
			}

		case c == '"':
			flush()
			// Block string """..."""
			if strings.HasPrefix(s[i:], `"""`) {
				i += 3
				if end := strings.Index(s[i:], `"""`); end >= 0 {
					i += end + 3
				} else {
					i = len(s)
				}
				continue
			}
			i++
			for i < len(s) {
				if s[i] == '\\' {
					i += 2
					continue
				}
				if s[i] == '"' {
					i++
					break
				}
				i++
			}

		case c == '{' || c == '}' || c == '(' || c == ')' || c == ':' || c == '@' || c == '[' || c == ']' || c == '$' || c == '!' || c == '=':
			flush()
			toks = append(toks, graphQLToken{text: string(c), punct: true})
			i++

		case c == ',' || c == ' ' || c == '\t' || c == '\n' || c == '\r':
			flush()
			i++

		case c == '.':
			flush()
			// Fragment spread "..."
			if strings.HasPrefix(s[i:], "...") {
				toks = append(toks, graphQLToken{text: "...", punct: true})
				i += 3
				continue
			}
			i++

		default:
			cur.WriteByte(c)
			i++
		}
	}
	flush()
	return toks
}

// graphQLOperationType returns the operation type and the index of the token
// that begins the operation (the keyword, or the opening brace for shorthand).
//
// A document may define fragments before the operation, so this skips over
// any `fragment Name on Type { ... }` blocks rather than reading the first
// brace it finds.
func graphQLOperationType(toks []graphQLToken) (hoopinspect.Operation, int) {
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.punct {
			// Shorthand: a bare selection set is an anonymous query.
			if t.text == "{" {
				return hoopinspect.OpQuery, i
			}
			continue
		}
		switch strings.ToLower(t.text) {
		case "query":
			return hoopinspect.OpQuery, i
		case "mutation":
			return hoopinspect.OpMutation, i
		case "subscription":
			return hoopinspect.OpSubscription, i
		case "fragment":
			// Skip the whole fragment definition.
			i = skipBracedBlock(toks, i)
		}
	}
	return hoopinspect.OpUnknown, 0
}

// graphQLOperationName returns the name following the operation keyword, when
// the document names its operation.
func graphQLOperationName(toks []graphQLToken, cursor int) string {
	if cursor >= len(toks) || toks[cursor].punct {
		return "" // shorthand, anonymous
	}
	if cursor+1 < len(toks) && !toks[cursor+1].punct {
		return toks[cursor+1].text
	}
	return ""
}

// graphQLRootFields returns the top-level selections of the operation — the
// resolvers actually invoked. For a mutation these are the writes.
//
// Arguments, directives and variable definitions are skipped, and only depth-1
// names inside the operation's selection set are collected. Aliases
// (`alias: realField`) resolve to the REAL field, because a policy denying
// `deleteUser` must not be evadable by writing `x: deleteUser`.
func graphQLRootFields(toks []graphQLToken, cursor int) []string {
	// Find the operation's opening brace, skipping variable definitions
	// ($id: ID!) and directives (@auth).
	open := -1
	depth := 0
	for i := cursor; i < len(toks); i++ {
		switch toks[i].text {
		case "(":
			depth++
		case ")":
			depth--
		case "{":
			if depth == 0 {
				open = i
			}
		}
		if open >= 0 {
			break
		}
	}
	if open < 0 {
		return nil
	}

	var out []string
	seen := map[string]bool{}
	braceDepth := 0
	parenDepth := 0

	for i := open; i < len(toks); i++ {
		t := toks[i]
		if t.punct {
			switch t.text {
			case "{":
				braceDepth++
			case "}":
				braceDepth--
				if braceDepth == 0 {
					return out // operation selection set closed
				}
			case "(":
				parenDepth++
			case ")":
				parenDepth--
			}
			continue
		}
		if braceDepth != 1 || parenDepth != 0 {
			continue // nested selection, or inside arguments
		}

		name := t.text

		// Alias: `alias: field` — the next tokens are ':' then the real name.
		if i+2 < len(toks) && toks[i+1].punct && toks[i+1].text == ":" && !toks[i+2].punct {
			name = toks[i+2].text
			i += 2
		}

		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// graphQLDepth returns the maximum brace nesting depth of the document.
//
// Deep nesting is the canonical GraphQL denial-of-service vector: a query can
// walk a cyclic schema (user -> friends -> user -> ...) and force the server
// to resolve exponentially many nodes. A depth limit is a policy every
// GraphQL deployment eventually needs, and it is invisible to method-and-path
// authorization.
func graphQLDepth(toks []graphQLToken) int {
	depth, max := 0, 0
	for _, t := range toks {
		if !t.punct {
			continue
		}
		switch t.text {
		case "{":
			depth++
			if depth > max {
				max = depth
			}
		case "}":
			depth--
		}
	}
	return max
}

// skipBracedBlock returns the index of the closing brace of the block that
// starts at or after i, or the last index when unbalanced.
func skipBracedBlock(toks []graphQLToken, i int) int {
	depth := 0
	for ; i < len(toks); i++ {
		switch toks[i].text {
		case "{":
			depth++
		case "}":
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(toks) - 1
}
