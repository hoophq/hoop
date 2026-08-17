package policy_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/policy"
)

// --- finding shape -------------------------------------------------------

// Answered is the question a policy asks most, and the one it most often gets
// wrong by testing a value instead: an absent value means "found nothing",
// "never ran", "budget spent" and "provider down" all at once.
func TestAnsweredOnlyForOKAndCached(t *testing.T) {
	answered := map[string]bool{
		policy.FindingOK:          true,
		policy.FindingCached:      true,
		policy.FindingSkipped:     false,
		policy.FindingUnavailable: false,
		policy.FindingError:       false,
	}
	for status, want := range answered {
		if got := (policy.Finding{Status: status}).Answered(); got != want {
			t.Errorf("Finding{Status: %q}.Answered() = %v, want %v", status, got, want)
		}
	}
}

// The ordering is what every producer folds two findings by, so a swap here
// makes one of them report healthy through an outage.
func TestFindingRankOrdersTheMostDegradedHighest(t *testing.T) {
	ascending := []string{
		policy.FindingOK,
		policy.FindingSkipped,
		policy.FindingUnavailable,
		policy.FindingError,
	}
	for i := 1; i < len(ascending); i++ {
		if policy.FindingRank(ascending[i]) <= policy.FindingRank(ascending[i-1]) {
			t.Errorf("FindingRank(%q) must outrank FindingRank(%q)", ascending[i], ascending[i-1])
		}
	}
	if policy.FindingRank(policy.FindingCached) != policy.FindingRank(policy.FindingOK) {
		t.Error("cached and ok must rank equal; a cache hit established the same thing a fresh run did")
	}
	// An unrecognized status ranks zero so a producer emitting a typo
	// never displaces a real error.
	if got := policy.FindingRank("nonsense"); got != 0 {
		t.Errorf("FindingRank(unknown) = %d, want 0", got)
	}
}

// A second rule that succeeded must not hide the first one's outage, or a
// policy reading the status fails open on a producer it cannot see.
func TestMergeKeepsTheMoreDegradedStatus(t *testing.T) {
	for _, tc := range []struct{ name, first, second, want string }{
		{"error survives a later success", policy.FindingError, policy.FindingOK, policy.FindingError},
		{"a later error displaces a success", policy.FindingOK, policy.FindingError, policy.FindingError},
		{"skipped survives a later cached", policy.FindingSkipped, policy.FindingCached, policy.FindingSkipped},
		{"unavailable beats skipped", policy.FindingSkipped, policy.FindingUnavailable, policy.FindingUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := policy.Finding{Status: tc.first}.Merge(policy.Finding{Status: tc.second})
			if got.Status != tc.want {
				t.Errorf("Status = %q, want %q", got.Status, tc.want)
			}
		})
	}
}

// The degraded side wins the status and must not erase what the answered side
// established. A policy told "one of your two rules broke" still wants the
// entities the other one found.
func TestMergeKeepsTheAnsweredSidesValues(t *testing.T) {
	answered := policy.Finding{
		Source: "pii", Rule: "no-cpf", Status: policy.FindingOK,
		Values: map[string]any{"entities": []string{"BR_CPF"}},
	}
	broken := policy.Finding{Source: "pii", Rule: "pii-wide", Status: policy.FindingError}

	got := answered.Merge(broken)
	if got.Status != policy.FindingError {
		t.Fatalf("Status = %q, want error", got.Status)
	}
	if !slices.Equal(got.Values["entities"].([]string), []string{"BR_CPF"}) {
		t.Errorf("Values = %v, want the answered side's entities kept", got.Values)
	}
	if got.Rule != "no-cpf" {
		t.Errorf("Rule = %q, want the rule that established the values", got.Rule)
	}
}

// The mirror case: an incumbent that never answered has nothing to protect,
// so a later answer fills it in rather than being dropped for ranking lower.
func TestMergeFillsValuesFromALaterAnswer(t *testing.T) {
	skipped := policy.Finding{Source: "pii", Rule: "quiet", Status: policy.FindingSkipped}
	answered := policy.Finding{
		Source: "pii", Rule: "no-cpf", Status: policy.FindingOK,
		Values: map[string]any{"entities": []string{"EMAIL_ADDRESS"}},
	}

	got := skipped.Merge(answered)
	if got.Status != policy.FindingSkipped {
		t.Fatalf("Status = %q, want skipped; an answer does not undo the skip", got.Status)
	}
	if !slices.Equal(got.Values["entities"].([]string), []string{"EMAIL_ADDRESS"}) {
		t.Errorf("Values = %v, want the answered side's entities", got.Values)
	}
	if got.Rule != "no-cpf" {
		t.Errorf("Rule = %q, want the rule that answered", got.Rule)
	}
}

// One entry per source is the whole contract of the channel: a Rego policy
// indexes input.findings by source, so two entries for one source would mean
// one of them is unreachable.
func TestAddFindingFoldsOneSource(t *testing.T) {
	var ec policy.EvalContext
	ec.AddFinding(policy.Finding{
		Source: "pii", Rule: "no-cpf", Status: policy.FindingOK,
		Values: map[string]any{"entities": []string{"BR_CPF"}},
	})
	ec.AddFinding(policy.Finding{Source: "pii", Rule: "pii-wide", Status: policy.FindingUnavailable})

	if len(ec.Findings) != 1 {
		t.Fatalf("Findings = %v, want one entry per source", ec.Findings)
	}
	f, ok := ec.Finding("pii")
	if !ok {
		t.Fatal("the source did not report at all")
	}
	if f.Status != policy.FindingUnavailable {
		t.Errorf("Status = %q, want unavailable", f.Status)
	}
	if f.Values["entities"] == nil {
		t.Errorf("the fold erased the answered rule's values: %v", f.Values)
	}
}

// Three-valued, which is why Requested is a map of bool rather than a set.
// Collapsing absent into false would veto every producer on a lane whose
// gate said nothing about it.
func TestWantsRunSeparatesSilenceFromAVeto(t *testing.T) {
	ec := policy.EvalContext{Requested: map[string]bool{"ai_analysis": false, "pii": true}}

	for _, tc := range []struct {
		source            string
		wantRun, wantSaid bool
	}{
		{"deny_words_list", false, false},
		{"ai_analysis", false, true},
		{"pii", true, true},
	} {
		run, said := ec.WantsRun(tc.source)
		if run != tc.wantRun || said != tc.wantSaid {
			t.Errorf("WantsRun(%q) = (%v, %v), want (%v, %v)",
				tc.source, run, said, tc.wantRun, tc.wantSaid)
		}
	}
}

// A producer may be handed no context at all (a direct Evaluate, a caller
// that never built one), and asking it a question must not panic on the data
// path.
func TestWantsRunOnANilContext(t *testing.T) {
	var ec *policy.EvalContext

	if run, said := ec.WantsRun("ai_analysis"); run || said {
		t.Errorf("WantsRun on a nil context = (%v, %v), want no opinion", run, said)
	}
	if _, ok := ec.Finding("ai_analysis"); ok {
		t.Error("a nil context reported a finding")
	}
}

// --- rules as producers --------------------------------------------------

// The point of deferring: the MATCHING stays local (microseconds, no network)
// and only the DETERMINATION moves to the policy. A deferring rule that
// denied would put the decision back where it was.
func TestDeferringRuleReportsInsteadOfDenying(t *testing.T) {
	rules, err := policy.NewRules([]policy.Rule{{
		Name: "no-destructive", Type: policy.MatchDenyWords,
		Words: []string{"DELETE"}, Action: policy.ActionDefer,
	}})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}

	var ec policy.EvalContext
	if v := rules.EvaluateWith(stmt("DELETE FROM customers", hoopinspect.OpDelete, "customers"), &ec); v.Denied {
		t.Fatalf("a deferring rule denied: %+v", v)
	}

	f, ok := ec.Finding(string(policy.MatchDenyWords))
	if !ok {
		t.Fatalf("the match was not reported: %v", ec.Findings)
	}
	if f.Status != policy.FindingOK {
		t.Errorf("Status = %q, want ok", f.Status)
	}
	if f.Rule != "no-destructive" {
		t.Errorf("Rule = %q, want the rule that matched", f.Rule)
	}
}

// Deferring does not stop the rule set. First match wins applies to DENIALS,
// so a hard rule behind a deferring one still enforces.
func TestDeferringRuleDoesNotStopALaterDenial(t *testing.T) {
	rules, err := policy.NewRules([]policy.Rule{
		{Name: "watch-deletes", Type: policy.MatchDenyWords,
			Words: []string{"DELETE"}, Action: policy.ActionDefer},
		{Name: "no-writes", Type: policy.MatchOperation,
			Operations: []hoopinspect.Operation{hoopinspect.OpDelete},
			Message:    "this credential is read-only"},
	})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}

	var ec policy.EvalContext
	v := rules.EvaluateWith(stmt("DELETE FROM customers", hoopinspect.OpDelete, "customers"), &ec)
	if !v.Denied {
		t.Fatal("a hard rule behind a deferring one stopped enforcing")
	}
	if v.Rule != "no-writes" {
		t.Errorf("Rule = %q, want no-writes", v.Rule)
	}
}

// Order still expresses precedence among the rules that deny; a deferring
// rule in front of them must not reorder anything.
func TestFirstMatchStillWinsAmongDenyingRules(t *testing.T) {
	rules, err := policy.NewRules([]policy.Rule{
		{Name: "watch", Type: policy.MatchDenyWords,
			Words: []string{"DELETE"}, Action: policy.ActionDefer},
		{Name: "first", Type: policy.MatchOperation,
			Operations: []hoopinspect.Operation{hoopinspect.OpDelete}, Message: "first"},
		{Name: "second", Type: policy.MatchDenyWords,
			Words: []string{"DELETE"}, Message: "second"},
	})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}

	var ec policy.EvalContext
	v := rules.EvaluateWith(stmt("DELETE FROM t", hoopinspect.OpDelete, "t"), &ec)
	if v.Rule != "first" {
		t.Errorf("Rule = %q, want first; order must express precedence", v.Rule)
	}
}

// A finding is a match, not a configuration. Reporting one for a rule that
// did not fire would have every policy see the whole rule set on every
// statement.
func TestNonMatchingDeferringRuleReportsNothing(t *testing.T) {
	rules, err := policy.NewRules([]policy.Rule{{
		Name: "watch-drops", Type: policy.MatchDenyWords,
		Words: []string{"DROP"}, Action: policy.ActionDefer,
	}})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}

	var ec policy.EvalContext
	rules.EvaluateWith(stmt("SELECT 1", hoopinspect.OpSelect), &ec)

	if len(ec.Findings) != 0 {
		t.Errorf("Findings = %v, want nothing from a rule that did not match", ec.Findings)
	}
}

// Findings are keyed by rule TYPE, because a policy asks "what did the PII
// scanner find", not "what did the rule named no-cpf find". Two rules of one
// type therefore fold, and overwriting instead of unioning would let the
// second rule's single entity class hide the first rule's.
func TestTwoPIIRulesFoldIntoOneFindingByUnion(t *testing.T) {
	s := fakeScanner{"BR_CPF": "111.222.333-44", "EMAIL_ADDRESS": "ada@example.com"}
	rules, err := policy.NewRulesWithScanner([]policy.Rule{
		{Name: "no-cpf", Type: policy.MatchPII,
			Entities: []string{"BR_CPF"}, Action: policy.ActionDefer},
		{Name: "pii-wide", Type: policy.MatchPII,
			Entities: []string{"EMAIL_ADDRESS"}, Action: policy.ActionDefer},
	}, s)
	if err != nil {
		t.Fatalf("NewRulesWithScanner: %v", err)
	}

	var ec policy.EvalContext
	rules.EvaluateWith(piiStmt("SELECT * FROM customers WHERE cpf = '111.222.333-44' AND email = 'ada@example.com'"), &ec)

	if len(ec.Findings) != 1 {
		t.Fatalf("Findings = %v, want one entry keyed by rule type", ec.Findings)
	}
	f, ok := ec.Finding(string(policy.MatchPII))
	if !ok {
		t.Fatalf("no pii finding: %v", ec.Findings)
	}

	entities := append([]string(nil), f.Values["entities"].([]string)...)
	slices.Sort(entities)
	if !slices.Equal(entities, []string{"BR_CPF", "EMAIL_ADDRESS"}) {
		t.Errorf("values.entities = %v, want the union of both rules", entities)
	}

	names := append([]string(nil), f.Values["rules"].([]string)...)
	slices.Sort(names)
	if !slices.Equal(names, []string{"no-cpf", "pii-wide"}) {
		t.Errorf("values.rules = %v, want both rule names", names)
	}
}

// Which of several words fired is the one thing a deny-words match tells a
// policy that input.statement does not: recovering it in Rego means
// reimplementing the matcher.
func TestDeferredDenyWordsNamesTheWordThatFired(t *testing.T) {
	rules, err := policy.NewRules([]policy.Rule{{
		Name: "watch-destructive", Type: policy.MatchDenyWords,
		Words: []string{"DROP", "TRUNCATE", "DELETE"}, Action: policy.ActionDefer,
	}})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}

	var ec policy.EvalContext
	rules.EvaluateWith(stmt("DELETE FROM customers", hoopinspect.OpDelete, "customers"), &ec)

	f, _ := ec.Finding(string(policy.MatchDenyWords))
	words, _ := f.Values["words"].([]string)
	if !slices.Equal(words, []string{"DELETE"}) {
		t.Errorf("values.words = %v, want only the word that fired", words)
	}
}

// A matched pattern's TEXT is content FROM the statement, and OPA's decision
// log is a copy of everything sent to it. Reporting the match would publish
// the taxpayer id the rule objected to into a second system's storage, which
// is exactly what the rule exists to prevent.
func TestDeferredPatternMatchNeverReportsTheMatchedText(t *testing.T) {
	const secret = "111.222.333-44"
	rules, err := policy.NewRules([]policy.Rule{{
		Name: "cpf-in-predicate", Type: policy.MatchPattern,
		Pattern: `cpf\s*=\s*'[^']+'`, Action: policy.ActionDefer,
	}})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}

	var ec policy.EvalContext
	rules.EvaluateWith(stmt("SELECT * FROM customers WHERE cpf = '"+secret+"'",
		hoopinspect.OpSelect, "customers"), &ec)

	f, ok := ec.Finding(string(policy.MatchPattern))
	if !ok {
		t.Fatalf("the match was not reported: %v", ec.Findings)
	}
	if rendered := fmt.Sprint(f.Values); strings.Contains(rendered, secret) {
		t.Fatalf("the finding carried statement content: %s", rendered)
	}
	for key := range f.Values {
		// Only the rule names may travel. Anything else on a pattern
		// rule can only have come out of the statement.
		if key != "rules" {
			t.Errorf("values carried %q; a pattern match reports the rule and nothing else", key)
		}
	}
}

// --- construction --------------------------------------------------------

// A typo'd action must fail at startup. Treating an unknown value as the
// empty default would silently turn an intended report into a denial.
func TestUnknownActionRejectedAtConstruction(t *testing.T) {
	_, err := policy.NewRules([]policy.Rule{{
		Name: "watch", Type: policy.MatchDenyWords,
		Words: []string{"DROP"}, Action: "warn",
	}})
	if err == nil {
		t.Fatal("NewRules accepted an unknown action")
	}
	if !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("error = %v, want it to name the bad action", err)
	}
}

// An ai_analysis rule defers per risk level through high/medium/low. Accepting
// action: defer beside those would leave two answers to one question, and the
// operator would not know which one runs.
func TestDeferOnAIAnalysisRuleRejectedAtConstruction(t *testing.T) {
	_, err := policy.NewRules([]policy.Rule{{
		Name: "risky-writes", Type: policy.MatchAIAnalysis,
		Trigger:  &policy.AITrigger{Operations: []hoopinspect.Operation{hoopinspect.OpDelete}},
		HighRisk: policy.ActionDefer,
		Action:   policy.ActionDefer,
	}})
	if err == nil {
		t.Fatal("NewRules accepted action: defer on an ai_analysis rule")
	}
	if !strings.Contains(err.Error(), "high/medium/low") {
		t.Errorf("error = %v, want it to point at the per-level spelling", err)
	}
}

// A finding recorded before a denial must survive it. What the scanner found
// is true whether or not a later rule refused the statement, and dropping it
// on the deny path would lose the reason a reviewer most wants.
func TestDeferredFindingSurvivesALaterDenial(t *testing.T) {
	rules, err := policy.NewRulesWithScanner([]policy.Rule{
		{Name: "cpf", Type: policy.MatchPII, Entities: []string{"BR_CPF"}, Action: policy.ActionDefer},
		{Name: "no-select", Type: policy.MatchOperation,
			Operations: []hoopinspect.Operation{hoopinspect.OpSelect}, Message: "no selects"},
	}, fakeScanner{"BR_CPF": "111.222.333-44"})
	if err != nil {
		t.Fatalf("NewRulesWithScanner: %v", err)
	}

	ec := &policy.EvalContext{}
	v := rules.EvaluateWith(piiStmt("SELECT * FROM t WHERE cpf = '111.222.333-44'"), ec)

	if !v.Denied {
		t.Fatalf("the hard rule did not deny: %+v", v)
	}
	f, ok := ec.Finding("pii")
	if !ok {
		t.Fatal("the deferred finding was dropped because something else denied")
	}
	if !f.Answered() {
		t.Errorf("Status = %q, want the scanner's answer intact", f.Status)
	}
}

// A deferring rule BELOW a denying one never runs, and that is first match
// wins rather than a lost finding: the rule set stops at the first denial, so
// a scan the operator placed after it is a scan they chose not to pay for.
func TestADenialStopsRulesBelowItFromReporting(t *testing.T) {
	rules, err := policy.NewRulesWithScanner([]policy.Rule{
		{Name: "no-select", Type: policy.MatchOperation,
			Operations: []hoopinspect.Operation{hoopinspect.OpSelect}, Message: "no selects"},
		{Name: "cpf", Type: policy.MatchPII, Entities: []string{"BR_CPF"}, Action: policy.ActionDefer},
	}, fakeScanner{"BR_CPF": "111.222.333-44"})
	if err != nil {
		t.Fatalf("NewRulesWithScanner: %v", err)
	}

	ec := &policy.EvalContext{}
	if v := rules.EvaluateWith(piiStmt("SELECT * FROM t WHERE cpf = '111.222.333-44'"), ec); !v.Denied {
		t.Fatalf("the first rule did not deny: %+v", v)
	}
	if _, ok := ec.Finding("pii"); ok {
		t.Error("a rule below the denial reported, so the set did not stop at the first match")
	}
}

// A table rule meaning "nothing WRITES customers" must not fire on a
// statement that only reads it. Before the access split the only expressible
// rule was "nothing mentions customers", which fires on both, and operators
// respond by widening the rule until it protects nothing.
func TestTableRuleAccessSplit(t *testing.T) {
	write := hoopinspect.Statement{
		Protocol: hoopinspect.Postgres, Direction: hoopinspect.FromClient,
		Text: "DELETE FROM customers", Operation: hoopinspect.OpDelete,
		Tables:    []string{"customers"},
		Relations: []hoopinspect.Relation{{Name: "customers", Access: hoopinspect.AccessWrite}},
	}
	read := hoopinspect.Statement{
		Protocol: hoopinspect.Postgres, Direction: hoopinspect.FromClient,
		Text: "INSERT INTO staging SELECT * FROM customers", Operation: hoopinspect.OpInsert,
		Tables: []string{"staging", "customers"},
		Relations: []hoopinspect.Relation{
			{Name: "staging", Access: hoopinspect.AccessWrite},
			{Name: "customers", Access: hoopinspect.AccessRead},
		},
	}

	mustRules := func(access string) *policy.Rules {
		t.Helper()
		r, err := policy.NewRules([]policy.Rule{{
			Name: "protect-customers", Type: policy.MatchTable,
			Tables: []string{"customers"}, Access: access,
			Message: "no",
		}})
		if err != nil {
			t.Fatalf("NewRules(%q): %v", access, err)
		}
		return r
	}

	if v := mustRules("write").Evaluate(write); !v.Denied {
		t.Error("access:write did not deny a write")
	}
	if v := mustRules("write").Evaluate(read); v.Denied {
		t.Error("access:write denied a statement that only READS customers")
	}
	if v := mustRules("read").Evaluate(read); !v.Denied {
		t.Error("access:read did not deny a read")
	}
	// An unset access is what every rule written before the split meant.
	if v := mustRules("").Evaluate(read); !v.Denied {
		t.Error("an access-less rule stopped matching; that breaks deployed configs")
	}
	if v := mustRules("").Evaluate(write); !v.Denied {
		t.Error("an access-less rule stopped matching a write")
	}
}

// An unknown access value is refused at construction rather than silently
// matching everything.
func TestUnknownAccessRejected(t *testing.T) {
	_, err := policy.NewRules([]policy.Rule{{
		Name: "bad", Type: policy.MatchTable,
		Tables: []string{"t"}, Access: "readwrite",
	}})
	if err == nil {
		t.Fatal("access:readwrite was accepted")
	}
	if !strings.Contains(err.Error(), "access") {
		t.Errorf("the error does not name the field: %v", err)
	}
}
