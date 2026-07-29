package policy

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hoophq/hoopinspect"
)

// MatchPII denies when the statement carries any of Entities, as reported by
// the Scanner wired into the rule set.
//
// This is the guardrail half of PII detection, and it is a different question
// from masking. Masking asks "rewrite this value on the way out". A guardrail
// asks "should this statement have been written at all" — a WHERE clause
// carrying a customer's national ID leaks that ID into every query log,
// slow-query log and EXPLAIN output the database keeps, and no amount of
// response masking undoes it.
//
// The rule is inert without a Scanner: NewRulesWithScanner wires one in, and
// NewRules rejects a PII rule outright rather than letting it silently allow
// everything. A guardrail that cannot see is worse than no guardrail, because
// someone believes it is working.
const MatchPII MatchType = "pii"

// Scanner reports which classes of sensitive data appear in a piece of text.
//
// Declared here as a narrow interface rather than imported from mask/ so the
// policy package keeps its own dependency surface and a caller can supply any
// detector — the built-in masker, an existing DLP service,
// github.com/hoophq/alcatraz — without policy knowing which.
//
// Implementations MUST be safe for concurrent use.
type Scanner interface {
	// ScanText returns the distinct entity names found in text, in any
	// order. Returning nil means "found nothing".
	//
	// It deliberately returns names and never values or offsets: a policy
	// verdict travels into an audit record, and a denial message quoting the
	// SSN it denied has published it.
	ScanText(text string) []string
}

// NewRulesWithScanner is NewRules with a Scanner available to MatchPII rules.
// A nil Scanner is exactly NewRules, and any PII rule then fails validation.
func NewRulesWithScanner(rules []Rule, s Scanner) (*Rules, error) {
	out, err := newRules(rules, s != nil)
	if err != nil {
		return nil, err
	}
	out.scanner = s
	return out, nil
}

// validatePII checks a MatchPII rule at construction.
func (r Rule) validatePII(hasScanner bool) error {
	if !hasScanner {
		return fmt.Errorf("%s: pii rule needs a scanner (use policy.NewRulesWithScanner)", r.Name)
	}
	if len(r.Entities) == 0 {
		return fmt.Errorf("%s: pii rule with no entities", r.Name)
	}
	return nil
}

// matchesPII reports whether the statement carries a denied entity, and which.
//
// It scans Statement.Text, which for SQL is the literal query the client sent
// and for HTTP is the request line. Bodies are NOT scanned here: a request
// body only reaches the policy layer when the codec was configured to capture
// it, and a rule that silently checks less than an operator assumes is the
// failure this comment exists to prevent. Mask the response for body PII.
func (r Rule) matchesPII(stmt hoopinspect.Statement, s Scanner) (bool, string) {
	if s == nil || stmt.Text == "" {
		return false, ""
	}
	found := s.ScanText(stmt.Text)
	if len(found) == 0 {
		return false, ""
	}

	want := make(map[string]bool, len(r.Entities))
	for _, e := range r.Entities {
		want[e] = true
	}

	var hit []string
	for _, f := range found {
		if want[f] {
			hit = append(hit, f)
		}
	}
	if len(hit) == 0 {
		return false, ""
	}
	// Sorted so the denial message is stable across runs: an operator
	// grepping their logs for one message should find every occurrence.
	sort.Strings(hit)
	return true, strings.Join(hit, ", ")
}

// piiMessage names the entity classes found without quoting their values.
func (r Rule) piiMessage(entities string) string {
	if r.Message != "" {
		return r.Message
	}
	return fmt.Sprintf("statement carries sensitive data (%s) and was denied by rule %q", entities, r.Name)
}
