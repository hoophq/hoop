package policy

import "github.com/hoophq/hoopinspect"

// Fluent builders for the HTTP rule fields.
//
// The fields are unexported (they live on the embedded httpRuleFields) so
// that a rule set loaded from JSON and one built in Go go through the same
// validation in NewRules. These setters are the Go-side constructor.
//
// They take and return Rule by value, so a rule literal reads as one
// expression:
//
//	policy.Rule{Name: "no-admin", Type: policy.MatchHTTPResource}.
//	    WithResources("/admin/**").
//	    WithMethods("POST", "DELETE")

// WithResources sets the resource patterns for a MatchHTTPResource rule.
// Patterns are matched against the NORMALIZED resource, so write
// "/users/*/ssn", not "/users/12345/ssn".
func (r Rule) WithResources(patterns ...string) Rule {
	r.Resources = patterns
	return r
}

// WithMethods narrows an HTTP rule to these methods. Empty means every
// method.
func (r Rule) WithMethods(methods ...string) Rule {
	r.Methods = methods
	return r
}

// WithStatuses sets the status specs for a MatchHTTPStatus rule. Accepts
// exact codes ("404") and classes ("4xx").
func (r Rule) WithStatuses(statuses ...string) Rule {
	r.Statuses = statuses
	return r
}

// WithGraphQLOperations sets the operation types for a MatchGraphQLOperation
// rule.
func (r Rule) WithGraphQLOperations(ops ...hoopinspect.Operation) Rule {
	r.GraphQLOperations = ops
	return r
}

// WithFields sets the root-field names for a MatchGraphQLField rule.
func (r Rule) WithFields(fields ...string) Rule {
	r.Fields = fields
	return r
}

// WithMaxDepth sets the nesting limit for a MatchGraphQLDepth rule. A
// statement whose depth EXCEEDS this is denied.
func (r Rule) WithMaxDepth(depth int) Rule {
	r.MaxDepth = depth
	return r
}

// RequiringGraphQL makes a GraphQL rule deny requests whose body could not be
// parsed as GraphQL, instead of letting them past every GraphQL rule.
func (r Rule) RequiringGraphQL() Rule {
	r.RequireGraphQL = true
	return r
}

// WithMessage sets the user-facing denial message.
func (r Rule) WithMessage(msg string) Rule {
	r.Message = msg
	return r
}
