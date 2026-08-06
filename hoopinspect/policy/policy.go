// Package policy turns an inspected Statement into an allow/deny verdict.
//
// Two evaluators ship here, and they layer:
//
//   - Rules: a local, dependency-free matcher (deny-words, regex, operation
//     and table allow/deny lists). Microseconds, no network, so it is safe on
//     the data path. Use it for the coarse "never, under any circumstances"
//     rules.
//   - OPA: a client for Open Policy Agent's Data API. Use it for policy an
//     InfoSec team already owns in Rego.
//
// # Deny messages reach the user
//
// Envoy's RBAC network filter denies by dropping the connection. The developer
// sees "connection reset" and files a ticket. Every deny path here carries an
// operator-authored Message, and the caller must surface it in the protocol's
// own error frame (a Postgres ErrorResponse, a MySQL ERR packet) so the user
// reads the reason and fixes it themselves.
//
// # Fail-closed
//
// Both evaluators fail closed on error by default: if OPA is unreachable or a
// rule cannot compile, the verdict is Deny with a diagnostic Message. Set
// FailOpen to invert that where availability outranks enforcement. Neither
// evaluator has a third mode.
package policy

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/hoophq/hoopinspect"
)

// Verdict is the outcome of evaluating one statement.
type Verdict struct {
	// Denied is the decision. False means the statement may proceed.
	Denied bool

	// Message reaches the end user when Denied. The operator authors it
	// where a rule supplies one, so it can name the actual constraint
	// ("destructive statements are not permitted on appdb") rather than
	// leaking rule internals.
	Message string

	// Rule identifies which rule produced a denial, for audit correlation.
	// Empty on allow.
	Rule string

	// Err holds the failure when evaluation itself broke (OPA unreachable,
	// bad regex). Denied reflects the fail-open/fail-closed choice; Err
	// records the cause.
	Err error

	// Annotations carry evaluator-specific facts for the audit record.
	// They never affect the decision: Denied is the whole of that.
	//
	// The field exists because the AI analyzer produces something no other
	// evaluator does — a risk level that is worth recording on an ALLOWED
	// statement. Widening Denied into a severity would change every
	// evaluator; a side channel the gate copies onto the event does not.
	//
	// Keys must be a small fixed vocabulary, because audit.SinkOptions
	// redaction does not reach Event.Metadata: anything put here bypasses
	// redact_statements and lands in the trail verbatim. Never put a value
	// the statement contained here.
	Annotations map[string]string
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
	// be extracted: "we could not tell" reads as unsafe.
	MatchTable MatchType = "table"

	// MatchAIAnalysis sends the statement to a language model and denies
	// according to the risk it reports.
	//
	// It is the only rule type Rules does NOT evaluate. The sidecar lifts
	// rules of this type out of the set before building Rules and turns
	// each one into an analyzer.Evaluator appended after the local rules
	// and OPA, so a statement a free rule already denies never reaches a
	// paid classifier. Rules rejects one that reaches it, because a rule
	// that parses and never fires is the failure this package refuses
	// everywhere else.
	MatchAIAnalysis MatchType = "ai_analysis"
)

// AITrigger narrows which statements an ai_analysis rule sends to a model.
//
// It is declared here rather than in the analyzer package so the config
// schema has one home: a Rule is what the YAML decodes into, and a trigger
// living elsewhere would mean two packages owning one config block.
type AITrigger struct {
	// Operations matches the statement's normalized verb.
	Operations []hoopinspect.Operation `json:"operations,omitempty"`

	// Tables matches any referenced table. Because Tables is best effort, a
	// statement whose tables could not be determined does NOT match; key a
	// load-bearing trigger on operations.
	Tables []string `json:"tables,omitempty"`

	// Resources matches an HTTP resource glob, using the same matcher an
	// http_resource rule uses.
	Resources []string `json:"resources,omitempty"`
}

// IsZero reports whether the trigger names nothing.
func (t *AITrigger) IsZero() bool {
	return t == nil || (len(t.Operations) == 0 && len(t.Tables) == 0 && len(t.Resources) == 0)
}

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

	// Message reaches the user on denial. Leave it empty and the rule falls
	// back to a generated message naming only the rule and the operation;
	// set it.
	Message string `json:"message,omitempty"`

	// Entities for MatchPII, naming the classes a Scanner must not find.
	Entities []string `json:"entities,omitempty"`

	// Trigger narrows which statements a MatchAIAnalysis rule classifies.
	// A rule with an empty trigger classifies nothing, because the failure
	// mode of "matches everything by accident" is a bill rather than an
	// error.
	Trigger *AITrigger `json:"trigger,omitempty"`

	// HighRisk, MediumRisk and LowRisk map a MatchAIAnalysis verdict onto
	// an action: allow, warn or block. An unset level defaults to allow, so
	// an operator opts into blocking a tier by naming it.
	HighRisk   string `json:"high,omitempty"`
	MediumRisk string `json:"medium,omitempty"`
	LowRisk    string `json:"low,omitempty"`

	// Prompt replaces the analyzer's default risk guidance for this rule.
	//
	// Per rule rather than per process, because what counts as risky is a
	// property of what a rule protects: a customer ledger and an orders API
	// want different wording, and both can sit in one config. This is also
	// the only place protocol-specific wording belongs — analyzer.prompt
	// reaches every lane, so SQL advice written there follows an HTTP
	// statement to the model. Empty inherits analyzer.prompt, and an empty
	// analyzer.prompt uses the built-in guidance, which covers both
	// protocols.
	//
	// It replaces the GUIDANCE only. The output contract — call exactly one
	// tool, never quote a literal value from the statement — is appended
	// after it and cannot be removed, because a title repeating the
	// identifier it objected to has published that identifier into the
	// audit trail.
	Prompt string `json:"prompt,omitempty"`

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

	// scanner backs MatchPII rules. It is nil unless NewRulesWithScanner set
	// it, and NewRules rejects PII rules while it is nil.
	scanner Scanner

	// FailOpen inverts the error behavior: when a rule cannot be evaluated
	// (an invalid regex), allow instead of deny. Default false.
	FailOpen bool
}

// NewRules compiles a rule set. It returns an error listing every rule that
// failed to compile, so a bad config surfaces at startup rather than on the
// first request that happens to hit it.
func NewRules(rules []Rule) (*Rules, error) { return newRules(rules, false) }

// newRules is the shared constructor. hasScanner tells PII validation whether
// a Scanner will be attached, so a PII rule without one fails at startup
// instead of quietly allowing every statement.
func newRules(rules []Rule, hasScanner bool) (*Rules, error) {
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
		case MatchPII:
			if err := r.validatePII(hasScanner); err != nil {
				problems = append(problems, err.Error())
			}
		case MatchAIAnalysis:
			// Rules cannot evaluate this type: it needs a provider, a
			// deadline and a cache, none of which belong to a local
			// matcher. The sidecar lifts these rules out before
			// building a Rules, so one arriving here means it was
			// constructed by a caller that does not know to do that,
			// and silently accepting it would leave a guardrail that
			// loads, evaluates and matches nothing.
			problems = append(problems, r.Name+
				": ai_analysis rules are evaluated by the analyzer, not by the local rule set")
		case MatchHTTPResource, MatchHTTPStatus:
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
		// Rules owns the Scanner, so PII dispatches here rather than
		// inside Rule.matches.
		if rule.Type == MatchPII {
			if hit, entities := rule.matchesPII(stmt, r.scanner); hit {
				return Deny(rule.Name, rule.piiMessage(entities))
			}
			continue
		}

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

// Chain evaluates every evaluator in order and returns the first denial.
//
// The intended order is local rules first, OPA second: a statement that a
// local rule already forbids never costs a network round-trip, and OPA stays
// off the hot path for the obvious cases.
//
// # Only a denial stops the chain
//
// An evaluator reports a broken evaluation through Err and decides for
// itself whether that means deny; a fail-open evaluator returns
// Denied=false with Err set. Treating a non-nil Err as a stop condition
// would let a fail-open evaluator suppress every evaluator behind it, so one
// unreachable OPA or one uncompilable regex would silently disable the rest
// of the policy -- the opposite of what an operator asked for by choosing
// fail-open for availability.
//
// So Err accumulates and evaluation continues. The errors travel on the
// returned Verdict either way, including on a denial, because the caller
// audits them and a degraded evaluator is worth recording even when a later
// one denied anyway.
type Chain []Evaluator

// Evaluate implements Evaluator.
func (c Chain) Evaluate(stmt hoopinspect.Statement) Verdict {
	var errs error
	var notes map[string]string
	for _, e := range c {
		v := e.Evaluate(stmt)
		// Annotations survive an allow, which is the point of them: the
		// analyzer's risk level belongs in the audit record whether or
		// not anything denied.
		for k, val := range v.Annotations {
			if notes == nil {
				notes = make(map[string]string, len(v.Annotations))
			}
			notes[k] = val
		}
		if v.Denied {
			v.Err = errors.Join(errs, v.Err)
			v.Annotations = notes
			return v
		}
		errs = errors.Join(errs, v.Err)
	}
	return Verdict{Err: errs, Annotations: notes}
}
