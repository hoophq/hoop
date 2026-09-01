package policy

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// MatchPII denies when the statement carries any of Entities, as reported by
// the Scanner wired into the rule set.
//
// It is the guardrail half of PII detection, and it asks a different question
// from masking. Masking rewrites a value on the way out. A guardrail
// asks whether the statement should have been written at all: a WHERE clause
// carrying a customer's national ID leaks that ID into every query log,
// slow-query log and EXPLAIN output the database keeps, and response masking
// cannot undo that.
//
// The rule is inert without a Scanner. NewRulesWithScanner wires one in, and
// NewRules rejects a PII rule outright rather than letting it silently allow
// everything: otherwise you run with a rule that permits every statement
// while believing enforcement is on.
const MatchPII MatchType = "pii"

// Scanner reports which classes of sensitive data appear in a piece of text.
//
// Declared here as a narrow interface rather than imported from mask/ so the
// policy package keeps its own dependency surface and you can supply any
// detector (the built-in masker, an existing DLP service,
// github.com/hoophq/alcatraz) without policy knowing which.
//
// Implementations MUST be safe for concurrent use.
type Scanner interface {
	// ScanText returns the distinct entity names found in text, in any
	// order. Returning nil means "found nothing".
	//
	// It returns names and never values or offsets, deliberately: a policy
	// verdict travels into an audit record, and a denial message quoting the
	// SSN it denied has published it.
	ScanText(text string) []string
}

// ScopedScanner is an optional Scanner that can narrow one scan to the entity
// classes the caller can act on.
//
// It exists because a detector's active set and a rule's Entities stopped
// being the same thing. A config that omits its pii section now gets a
// detector with every supported entity class active, and ScanText runs the
// detector's whole set: a rule naming two classes would pay for fifty-odd
// recognizer passes per statement and then discard all but two in the
// intersection below.
//
// It sits beside Scanner rather than inside it. Widening a one-method
// interface breaks every implementor, and this buys throughput rather than a
// capability the policy layer depends on. A scanner that does not implement
// it stays correct and stays slower.
type ScopedScanner interface {
	// ScanTextFor returns the distinct entity names found in text,
	// restricted to entities. The return contract matches ScanText: names
	// only, never values or offsets, in any order.
	//
	// An implementation MAY over-report, so a caller still filters. An
	// empty entities slice means the same as ScanText.
	ScanTextFor(entities []string, text string) []string
}

// NewRulesWithScanner is NewRules with a Scanner available to MatchPII rules.
// A nil Scanner behaves as NewRules, so any PII rule then fails validation.
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
// and for HTTP is the request line. It does NOT scan bodies: a request body
// only reaches the policy layer when the codec was configured to capture it,
// so a body-scanning rule would check less than you assume. Mask the response
// for body PII.
func (r Rule) matchesPII(stmt inspect.Statement, s Scanner) (bool, []string) {
	if s == nil || stmt.Text == "" {
		return false, nil
	}
	// Ask the detector for this rule's classes where it can be asked. The
	// intersection below still runs and must stay: ScanTextFor is allowed
	// to over-report, and a plain Scanner arrives here having scanned its
	// whole active set.
	var found []string
	if ss, ok := s.(ScopedScanner); ok {
		found = ss.ScanTextFor(r.Entities, stmt.Text)
	} else {
		found = s.ScanText(stmt.Text)
	}
	if len(found) == 0 {
		return false, nil
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
		return false, nil
	}
	// Sorted so the denial message is stable across runs (grep your logs
	// for one message and you find every occurrence) and so a deferred
	// finding hashes the same for a policy's decision log.
	sort.Strings(hit)
	return true, hit
}

// piiMessage names the entity classes found without quoting their values.
func (r Rule) piiMessage(entities []string) string {
	if r.Message != "" {
		return r.Message
	}
	return fmt.Sprintf("statement carries sensitive data (%s) and was denied by rule %q",
		strings.Join(entities, ", "), r.Name)
}
