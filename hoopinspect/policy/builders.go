package policy

// Fluent builders for the HTTP rule fields.
//
// The embedded httpRuleFields keeps these fields off the Rule literal's own
// surface, so a rule set loaded from JSON and one built in Go run through the
// same validation in NewRules. These setters are the Go-side constructor.
//
// They take and return Rule by value, so a rule literal reads as one
// expression:
//
//	policy.Rule{Name: "no-admin", Type: policy.MatchHTTPResource}.
//	    WithResources("/admin/**").
//	    WithMethods("POST", "DELETE")

// WithResources sets the resource patterns for a MatchHTTPResource rule.
// Patterns match the NORMALIZED resource, so write "/users/*/ssn", not
// "/users/12345/ssn".
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

// WithMessage sets the user-facing denial message.
func (r Rule) WithMessage(msg string) Rule {
	r.Message = msg
	return r
}
