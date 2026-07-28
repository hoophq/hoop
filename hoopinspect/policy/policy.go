// Package policy turns an inspected Statement into an allow/deny verdict.
//
// Two evaluators ship here, and they are meant to be layered:
//
//   - Rules  — a local, dependency-free matcher (deny-words, regex, operation
//     and table allow/deny lists). Microseconds, no network, so it is safe on
//     the data path. Use it for the coarse "never, under any circumstances"
//     rules.
//   - OPA    — a client for Open Policy Agent's Data API. Use it for policy
//     an InfoSec team already owns in Rego.
//
// # Why deny messages matter
//
// Envoy's RBAC network filter denies by dropping the connection. The developer
// sees "connection reset" and files a ticket. Every deny path here carries an
// operator-authored Message, and the caller is expected to surface it in the
// protocol's own error frame (a Postgres ErrorResponse, a MySQL ERR packet) so
// the user reads *why* and fixes it themselves.
//
// # Fail-closed
//
// Both evaluators fail closed on error by default: if OPA is unreachable or a
// rule cannot compile, the verdict is Deny with a diagnostic Message. Set
// FailOpen to invert that where availability outranks enforcement. There is no
// silent middle ground.
package policy

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/hoophq/hoopinspect"
)

// Verdict is the outcome of evaluating one statement.
type Verdict struct {
	// Denied is the decision. False means the statement may proceed.
	Denied bool

	// Message is shown to the end user when Denied. It is operator-authored
	// where a rule supplies one, so it can name the actual constraint
	// ("destructive statements are not permitted on appdb") rather than
	// leaking rule internals.
	Message string

	// Rule identifies which rule produced a denial, for audit correlation.
	// Empty on allow.
	Rule string

	// Err is set when evaluation itself failed (OPA unreachable, bad regex).
	// Denied reflects the fail-open/fail-closed choice; Err records why.
	Err error
}

// Allow is the zero verdict.
func Allow() Verdict { return Verdict{} }

// Deny builds a denial carrying a user-facing message.
func Deny(rule, msg string) Verdict {
	return Verdict{Denied: true, Message: msg, Rule: rule}
}

// Evaluator produces a verdict for a statement. Rules and OPAClient both
// implement it, and Chain composes them.
type Evaluator interface {
	Evaluate(stmt hoopinspect.Statement) Verdict
}

// --- local rules ---------------------------------------------------------

// MatchType selects how a Rule matches.
type MatchType string

const (
	// MatchDenyWords denies when the statement text contains any of Words,
	// case-insensitively. This is the blunt instrument: "no DROP, ever".
	MatchDenyWords MatchType = "deny_words_list"

	// MatchPattern denies when Pattern matches the statement text.
	MatchPattern MatchType = "pattern_match"

	// MatchOperation denies when the normalized operation is in Operations.
	// Prefer this over deny-words for verbs: it is immune to a DELETE hiding
	// in a string literal or a comment, because Operation comes from the
	// classifier, which strips both.
	MatchOperation MatchType = "operation"

	// MatchTable denies when the statement references any table in Tables.
	// Because Tables is best-effort (see hoopinspect.ClassifySQL), a rule of
	// this type also denies when RequireTableMatch is set and no tables could
	// be extracted — "we could not tell" reads as unsafe.
	MatchTable MatchType = "table"
)

// Rule is one local matcher.
type Rule struct {
	// Name identifies the rule in Verdict.Rule and in audit output.
	Name string `json:"name"`

	// Type selects the matching strategy.
	Type MatchType `json:"type"`

	// Words for MatchDenyWords.
	Words []string `json:"words,omitempty"`

	// Pattern for MatchPattern, an RE2 regular expression.
	Pattern string `json:"pattern_regex,omitempty"`

	// Operations for MatchOperation.
	Operations []hoopinspect.Operation `json:"operations,omitempty"`

	// Tables for MatchTable, compared lowercased. A bare name matches any
	// schema qualification: "customers" matches "public.customers".
	Tables []string `json:"tables,omitempty"`

	// RequireTableMatch makes a MatchTable rule also deny statements whose
	// tables could not be determined. Set it when the rule protects something
	// that must never be touched, and accept the false positives.
	RequireTableMatch bool `json:"require_table_match,omitempty"`

	// Message is shown to the user on denial. When empty a generic message is
	// generated, which is worse for everyone; set it.
	Message string `json:"message,omitempty"`

	// HTTP-specific fields (Resources, Statuses, Fields, MaxDepth, ...).
	// Embedded so one ordered rule set can mix SQL and HTTP matchers; a
	// deployment fronting both a database and an API should not need two
	// evaluators with two orderings.
	httpRuleFields

	// compiled caches the Pattern regex.
	compiled *regexp.Regexp
}

// Rules is an ordered set of local rules. The first match wins, so order
// expresses precedence.
type Rules struct {
	rules []Rule

	// FailOpen inverts the error behavior: when a rule cannot be evaluated
	// (an invalid regex), allow instead of deny. Default false.
	FailOpen bool
}

// NewRules compiles a rule set. It returns an error listing every rule that
// failed to compile, so a bad config surfaces at startup rather than on the
// first request that happens to hit it.
func NewRules(rules []Rule) (*Rules, error) {
	var problems []string
	out := make([]Rule, 0, len(rules))

	for i, r := range rules {
		if r.Name == "" {
			r.Name = fmt.Sprintf("rule[%d]", i)
		}
		switch r.Type {
		case MatchDenyWords:
			if len(r.Words) == 0 {
				problems = append(problems, r.Name+": deny_words_list with no words")
			}
		case MatchPattern:
			if r.Pattern == "" {
				problems = append(problems, r.Name+": pattern_match with no pattern")
				break
			}
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: bad pattern: %v", r.Name, err))
				break
			}
			r.compiled = re
		case MatchOperation:
			if len(r.Operations) == 0 {
				problems = append(problems, r.Name+": operation rule with no operations")
			}
		case MatchTable:
			if len(r.Tables) == 0 {
				problems = append(problems, r.Name+": table rule with no tables")
			}
		case MatchHTTPResource, MatchHTTPStatus, MatchGraphQLOperation,
			MatchGraphQLField, MatchGraphQLDepth:
			if err := r.validateHTTP(); err != nil {
				problems = append(problems, err.Error())
			}
		default:
			problems = append(problems, fmt.Sprintf("%s: unknown rule type %q", r.Name, r.Type))
		}
		out = append(out, r)
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("policy: invalid rules: %s", strings.Join(problems, "; "))
	}
	return &Rules{rules: out}, nil
}

// Evaluate implements Evaluator. First matching rule wins.
func (r *Rules) Evaluate(stmt hoopinspect.Statement) Verdict {
	for _, rule := range r.rules {
		matched, err := rule.matches(stmt)
		if err != nil {
			if r.FailOpen {
				return Verdict{Err: err}
			}
			return Verdict{
				Denied:  true,
				Message: "policy evaluation failed; denying",
				Rule:    rule.Name,
				Err:     err,
			}
		}
		if matched {
			return Deny(rule.Name, rule.messageOr(stmt))
		}
	}
	return Allow()
}

func (r Rule) messageOr(stmt hoopinspect.Statement) string {
	if r.Message != "" {
		return r.Message
	}
	return fmt.Sprintf("statement denied by policy rule %q (operation=%s)", r.Name, stmt.Operation)
}

func (r Rule) matches(stmt hoopinspect.Statement) (bool, error) {
	// HTTP rule types are handled in http.go; ok=false means "not mine".
	if matched, ok := r.matchesHTTP(stmt); ok {
		return matched, nil
	}
	switch r.Type {
	case MatchDenyWords:
		upper := strings.ToUpper(stmt.Text)
		for _, w := range r.Words {
			if w == "" {
				continue
			}
			if strings.Contains(upper, strings.ToUpper(w)) {
				return true, nil
			}
		}
		return false, nil

	case MatchPattern:
		if r.compiled == nil {
			return false, fmt.Errorf("policy: rule %q has no compiled pattern", r.Name)
		}
		return r.compiled.MatchString(stmt.Text), nil

	case MatchOperation:
		for _, op := range r.Operations {
			if stmt.Operation == op {
				return true, nil
			}
		}
		return false, nil

	case MatchTable:
		if len(stmt.Tables) == 0 {
			// Could not determine the tables. Deny only when the rule opted
			// into that strictness.
			return r.RequireTableMatch, nil
		}
		for _, want := range r.Tables {
			want = strings.ToLower(want)
			for _, got := range stmt.Tables {
				if got == want || strings.HasSuffix(got, "."+want) {
					return true, nil
				}
			}
		}
		return false, nil
	}
	return false, fmt.Errorf("policy: unknown rule type %q", r.Type)
}

// --- chaining ------------------------------------------------------------

// Chain evaluates each evaluator in order and returns the first denial.
//
// The intended order is local rules first, OPA second: a statement that a
// local rule already forbids never costs a network round-trip, and OPA stays
// off the hot path for the obvious cases.
type Chain []Evaluator

// Evaluate implements Evaluator.
func (c Chain) Evaluate(stmt hoopinspect.Statement) Verdict {
	for _, e := range c {
		if v := e.Evaluate(stmt); v.Denied || v.Err != nil {
			return v
		}
	}
	return Allow()
}
