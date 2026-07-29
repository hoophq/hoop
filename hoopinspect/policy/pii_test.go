package policy_test

import (
	"strings"
	"testing"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/policy"
)

// fakeScanner reports an entity when its literal appears in the text.
type fakeScanner map[string]string // entity -> literal

func (f fakeScanner) ScanText(text string) []string {
	var out []string
	for entity, lit := range f {
		if strings.Contains(text, lit) {
			out = append(out, entity)
		}
	}
	return out
}

func piiStmt(sql string) hoopinspect.Statement {
	return hoopinspect.Statement{
		Protocol:  hoopinspect.Postgres,
		Direction: hoopinspect.FromClient,
		Operation: hoopinspect.OpSelect,
		Text:      sql,
	}
}

func TestPIIRuleDeniesOnListedEntity(t *testing.T) {
	s := fakeScanner{"US_SSN": "123-45-6789"}
	rules, err := policy.NewRulesWithScanner([]policy.Rule{{
		Name:     "no-ssn-in-query",
		Type:     policy.MatchPII,
		Entities: []string{"US_SSN"},
		Message:  "do not put a national ID in a WHERE clause",
	}}, s)
	if err != nil {
		t.Fatalf("NewRulesWithScanner: %v", err)
	}

	v := rules.Evaluate(piiStmt("SELECT * FROM t WHERE ssn = '123-45-6789'"))
	if !v.Denied {
		t.Fatal("statement carrying an SSN was allowed")
	}
	if v.Message != "do not put a national ID in a WHERE clause" {
		t.Errorf("operator message not used: %q", v.Message)
	}
	if v.Rule != "no-ssn-in-query" {
		t.Errorf("Rule = %q", v.Rule)
	}
}

// An entity the scanner finds but the rule does not list must not deny.
// Denying on anything sensitive anywhere produces a guardrail no team can
// deploy.
func TestPIIRuleIgnoresUnlistedEntity(t *testing.T) {
	s := fakeScanner{"EMAIL_ADDRESS": "ada@example.com"}
	rules, err := policy.NewRulesWithScanner([]policy.Rule{{
		Name:     "no-ssn",
		Type:     policy.MatchPII,
		Entities: []string{"US_SSN"},
	}}, s)
	if err != nil {
		t.Fatalf("NewRulesWithScanner: %v", err)
	}

	if v := rules.Evaluate(piiStmt("SELECT * FROM t WHERE e = 'ada@example.com'")); v.Denied {
		t.Errorf("unlisted entity denied: %+v", v)
	}
}

// The generated message must name the classes found and never quote the value
// that triggered it: a verdict travels into the audit record.
func TestPIIDefaultMessageNamesClassNotValue(t *testing.T) {
	s := fakeScanner{"US_SSN": "123-45-6789"}
	rules, err := policy.NewRulesWithScanner([]policy.Rule{{
		Name:     "r",
		Type:     policy.MatchPII,
		Entities: []string{"US_SSN"},
	}}, s)
	if err != nil {
		t.Fatalf("NewRulesWithScanner: %v", err)
	}

	v := rules.Evaluate(piiStmt("SELECT '123-45-6789'"))
	if !v.Denied {
		t.Fatal("want denial")
	}
	if !strings.Contains(v.Message, "US_SSN") {
		t.Errorf("message should name the entity class: %q", v.Message)
	}
	if strings.Contains(v.Message, "123-45-6789") {
		t.Errorf("denial message leaked the value it denied: %q", v.Message)
	}
}

// Several hits are reported sorted, keeping an operator's log query stable.
func TestPIIMessageListsEntitiesSorted(t *testing.T) {
	s := fakeScanner{"US_SSN": "123-45-6789", "CREDIT_CARD": "4111111111111111"}
	rules, err := policy.NewRulesWithScanner([]policy.Rule{{
		Name:     "r",
		Type:     policy.MatchPII,
		Entities: []string{"US_SSN", "CREDIT_CARD"},
	}}, s)
	if err != nil {
		t.Fatalf("NewRulesWithScanner: %v", err)
	}

	v := rules.Evaluate(piiStmt("SELECT '123-45-6789', '4111111111111111'"))
	if !strings.Contains(v.Message, "CREDIT_CARD, US_SSN") {
		t.Errorf("entities not sorted in message: %q", v.Message)
	}
}

// A PII rule without a scanner must fail at construction. Otherwise the
// process starts up and silently allows everything.
func TestPIIRuleWithoutScannerRejected(t *testing.T) {
	_, err := policy.NewRules([]policy.Rule{{
		Name: "r", Type: policy.MatchPII, Entities: []string{"US_SSN"},
	}})
	if err == nil {
		t.Fatal("want error: a pii rule needs a scanner")
	}
	if !strings.Contains(err.Error(), "scanner") {
		t.Errorf("error should name the missing scanner: %v", err)
	}
}

func TestPIIRuleWithoutEntitiesRejected(t *testing.T) {
	s := fakeScanner{"US_SSN": "x"}
	_, err := policy.NewRulesWithScanner([]policy.Rule{{
		Name: "r", Type: policy.MatchPII,
	}}, s)
	if err == nil {
		t.Fatal("want error: a pii rule with no entities matches nothing")
	}
}

// A nil scanner degrades NewRulesWithScanner to NewRules, rejection included.
// There is no "scanner-shaped nil" path that silently allows.
func TestNilScannerBehavesAsNewRules(t *testing.T) {
	_, err := policy.NewRulesWithScanner([]policy.Rule{{
		Name: "r", Type: policy.MatchPII, Entities: []string{"US_SSN"},
	}}, nil)
	if err == nil {
		t.Fatal("want error for a pii rule with a nil scanner")
	}
}

// Rule order is precedence, so a PII rule takes its turn: it neither jumps
// the queue nor gets skipped.
func TestPIIRuleRespectsOrdering(t *testing.T) {
	s := fakeScanner{"US_SSN": "123-45-6789"}
	rules, err := policy.NewRulesWithScanner([]policy.Rule{
		{Name: "first", Type: policy.MatchOperation,
			Operations: []hoopinspect.Operation{hoopinspect.OpSelect},
			Message:    "no selects"},
		{Name: "second", Type: policy.MatchPII, Entities: []string{"US_SSN"}},
	}, s)
	if err != nil {
		t.Fatalf("NewRulesWithScanner: %v", err)
	}

	v := rules.Evaluate(piiStmt("SELECT '123-45-6789'"))
	if v.Rule != "first" {
		t.Errorf("earlier rule should win, got %q", v.Rule)
	}
}

// A clean statement passes, and a PII rule must not deny on an empty text.
func TestPIIAllowsCleanStatements(t *testing.T) {
	s := fakeScanner{"US_SSN": "123-45-6789"}
	rules, err := policy.NewRulesWithScanner([]policy.Rule{{
		Name: "r", Type: policy.MatchPII, Entities: []string{"US_SSN"},
	}}, s)
	if err != nil {
		t.Fatalf("NewRulesWithScanner: %v", err)
	}

	for _, sql := range []string{"SELECT 1", "SELECT name FROM customers", ""} {
		if v := rules.Evaluate(piiStmt(sql)); v.Denied {
			t.Errorf("%q denied: %+v", sql, v)
		}
	}
}

// Mixing PII with SQL and HTTP rules in one ordered set must work, which is
// the whole reason the rule types share a Rules.
func TestPIIMixesWithOtherRuleTypes(t *testing.T) {
	s := fakeScanner{"US_SSN": "123-45-6789"}
	rules, err := policy.NewRulesWithScanner([]policy.Rule{
		{Name: "no-drop", Type: policy.MatchOperation,
			Operations: []hoopinspect.Operation{hoopinspect.OpDrop}},
		policy.Rule{Name: "no-admin", Type: policy.MatchHTTPResource}.
			WithResources("/admin/**"),
		{Name: "no-ssn", Type: policy.MatchPII, Entities: []string{"US_SSN"}},
	}, s)
	if err != nil {
		t.Fatalf("NewRulesWithScanner: %v", err)
	}

	if v := rules.Evaluate(piiStmt("SELECT '123-45-6789'")); v.Rule != "no-ssn" {
		t.Errorf("pii rule did not fire in a mixed set: %+v", v)
	}
	if v := rules.Evaluate(piiStmt("SELECT 1")); v.Denied {
		t.Errorf("clean statement denied in a mixed set: %+v", v)
	}
}
