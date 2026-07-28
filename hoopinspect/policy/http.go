package policy

import (
	"fmt"
	"strings"

	"github.com/hoophq/hoopinspect"
)

// HTTP-specific rule types. They are separate from the SQL matchers because
// the useful questions differ: SQL policy asks "which verb, which table",
// HTTP policy asks "which resource, which status, which GraphQL operation".
const (
	// MatchHTTPResource denies when the normalized resource path matches any
	// entry in Resources. Patterns use the normalized form, so
	// "/users/*/ssn" matches /users/12345/ssn and /users/67890/ssn with one
	// rule instead of a regex per id.
	//
	// A trailing "/**" matches any deeper path: "/admin/**" covers
	// /admin/users and /admin/users/1/roles.
	MatchHTTPResource MatchType = "http_resource"

	// MatchHTTPStatus denies when the response status is in Statuses, or
	// falls in a class named as "2xx".."5xx". Response-side only — request
	// statements have StatusCode 0 and never match.
	//
	// This is the rule Envoy's ext_authz structurally cannot express: it
	// decides before the upstream is called.
	MatchHTTPStatus MatchType = "http_status"

	// MatchGraphQLOperation denies by GraphQL operation type
	// (query / mutation / subscription). This is the coarse GraphQL gate:
	// "this credential may read but not mutate".
	MatchGraphQLOperation MatchType = "graphql_operation"

	// MatchGraphQLField denies when any root field of the operation is in
	// Fields, matched case-insensitively. This is the fine gate: "nobody
	// calls deleteUser through this proxy".
	MatchGraphQLField MatchType = "graphql_field"

	// MatchGraphQLDepth denies when the selection-set nesting exceeds
	// MaxDepth. Deep nesting on a cyclic schema is the standard GraphQL
	// denial-of-service vector.
	MatchGraphQLDepth MatchType = "graphql_depth"
)

// HTTP-specific Rule fields. They live on the same Rule struct so a single
// ordered rule set can mix SQL and HTTP matchers — a chain in front of a
// mixed workload should not need two evaluators.
type httpRuleFields struct {
	// Resources for MatchHTTPResource. Compared against
	// Statement.HTTP.Resource. Supports a trailing "/**" wildcard.
	Resources []string `json:"resources,omitempty"`

	// Methods optionally narrows any HTTP rule to these methods. Empty means
	// every method. Applies to MatchHTTPResource and MatchHTTPStatus.
	Methods []string `json:"methods,omitempty"`

	// Statuses for MatchHTTPStatus. Accepts exact codes ("404") and classes
	// ("4xx", "5xx").
	Statuses []string `json:"statuses,omitempty"`

	// GraphQLOperations for MatchGraphQLOperation.
	GraphQLOperations []hoopinspect.Operation `json:"graphql_operations,omitempty"`

	// Fields for MatchGraphQLField.
	Fields []string `json:"fields,omitempty"`

	// MaxDepth for MatchGraphQLDepth. A statement whose depth EXCEEDS this
	// is denied.
	MaxDepth int `json:"max_depth,omitempty"`

	// RequireGraphQL makes a GraphQL rule also deny a request whose body did
	// not parse as GraphQL, when it was sent to a path the operator expects
	// to be a GraphQL endpoint. Without it, an unparseable body slips past
	// every GraphQL rule.
	RequireGraphQL bool `json:"require_graphql,omitempty"`
}

// validateHTTP checks the HTTP-specific fields of a rule at construction.
func (r Rule) validateHTTP() error {
	switch r.Type {
	case MatchHTTPResource:
		if len(r.Resources) == 0 {
			return fmt.Errorf("%s: http_resource rule with no resources", r.Name)
		}
	case MatchHTTPStatus:
		if len(r.Statuses) == 0 {
			return fmt.Errorf("%s: http_status rule with no statuses", r.Name)
		}
		for _, s := range r.Statuses {
			if !validStatusSpec(s) {
				return fmt.Errorf("%s: invalid status %q (want a code like 404 or a class like 4xx)", r.Name, s)
			}
		}
	case MatchGraphQLOperation:
		if len(r.GraphQLOperations) == 0 {
			return fmt.Errorf("%s: graphql_operation rule with no operations", r.Name)
		}
	case MatchGraphQLField:
		if len(r.Fields) == 0 {
			return fmt.Errorf("%s: graphql_field rule with no fields", r.Name)
		}
	case MatchGraphQLDepth:
		if r.MaxDepth <= 0 {
			return fmt.Errorf("%s: graphql_depth rule needs a positive max_depth", r.Name)
		}
	}
	return nil
}

// matchesHTTP evaluates the HTTP rule types. ok reports whether this rule type
// was handled here, so the SQL matcher can take everything else.
func (r Rule) matchesHTTP(stmt hoopinspect.Statement) (matched, ok bool) {
	switch r.Type {
	case MatchHTTPResource, MatchHTTPStatus, MatchGraphQLOperation,
		MatchGraphQLField, MatchGraphQLDepth:
	default:
		return false, false
	}

	d := stmt.HTTP
	if d == nil {
		// A non-HTTP statement can never match an HTTP rule. Failing closed
		// here would deny every SQL statement in a mixed rule set.
		return false, true
	}

	switch r.Type {
	case MatchHTTPResource:
		if !r.methodAllowed(d.Method) {
			return false, true
		}
		for _, pattern := range r.Resources {
			if matchResource(pattern, d.Resource) {
				return true, true
			}
		}
		return false, true

	case MatchHTTPStatus:
		if d.StatusCode == 0 {
			return false, true // a request, not a response
		}
		if !r.methodAllowed(d.Method) {
			return false, true
		}
		for _, spec := range r.Statuses {
			if matchStatus(spec, d.StatusCode) {
				return true, true
			}
		}
		return false, true

	case MatchGraphQLOperation:
		if d.GraphQL == nil {
			return r.RequireGraphQL, true
		}
		for _, op := range r.GraphQLOperations {
			if d.GraphQL.OperationType == op {
				return true, true
			}
		}
		return false, true

	case MatchGraphQLField:
		if d.GraphQL == nil {
			return r.RequireGraphQL, true
		}
		for _, want := range r.Fields {
			want = strings.ToLower(want)
			for _, got := range d.GraphQL.RootFields {
				if strings.ToLower(got) == want {
					return true, true
				}
			}
		}
		return false, true

	case MatchGraphQLDepth:
		if d.GraphQL == nil {
			return r.RequireGraphQL, true
		}
		return d.GraphQL.Depth > r.MaxDepth, true
	}
	return false, true
}

func (r Rule) methodAllowed(method string) bool {
	if len(r.Methods) == 0 {
		return true
	}
	for _, m := range r.Methods {
		if strings.EqualFold(m, method) {
			return true
		}
	}
	return false
}

// matchResource compares a normalized resource against a pattern.
//
// "*" matches exactly one segment; a trailing "/**" matches one or more
// trailing segments. Both sides are already normalized, so a pattern written
// with a literal id ("/users/12345") will not match — that is intentional, it
// forces patterns to be written against the stable resource form.
func matchResource(pattern, resource string) bool {
	if pattern == resource {
		return true
	}
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return resource == prefix || strings.HasPrefix(resource, prefix+"/")
	}

	pSegs := strings.Split(strings.Trim(pattern, "/"), "/")
	rSegs := strings.Split(strings.Trim(resource, "/"), "/")
	if len(pSegs) != len(rSegs) {
		return false
	}
	for i := range pSegs {
		if pSegs[i] == "*" {
			continue
		}
		if !strings.EqualFold(pSegs[i], rSegs[i]) {
			return false
		}
	}
	return true
}

// matchStatus compares a status spec ("404", "4xx") against a code.
func matchStatus(spec string, code int) bool {
	spec = strings.ToLower(strings.TrimSpace(spec))
	if len(spec) == 3 && spec[1] == 'x' && spec[2] == 'x' {
		class := int(spec[0]-'0') * 100
		return code >= class && code < class+100
	}
	want := 0
	for i := range len(spec) {
		if spec[i] < '0' || spec[i] > '9' {
			return false
		}
		want = want*10 + int(spec[i]-'0')
	}
	return want == code
}

func validStatusSpec(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) != 3 {
		return false
	}
	if s[0] < '1' || s[0] > '5' {
		return false
	}
	if s[1] == 'x' && s[2] == 'x' {
		return true
	}
	return s[1] >= '0' && s[1] <= '9' && s[2] >= '0' && s[2] <= '9'
}
