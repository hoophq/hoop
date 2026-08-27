package policy_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

func stmt(text string, op inspect.Operation, tables ...string) inspect.Statement {
	return inspect.Statement{
		Protocol:  inspect.Postgres,
		Direction: inspect.FromClient,
		Text:      text,
		Operation: op,
		Tables:    tables,
	}
}

func TestDenyWords(t *testing.T) {
	rules, err := policy.NewRules([]policy.Rule{{
		Name:    "no-destructive",
		Type:    policy.MatchDenyWords,
		Words:   []string{"DROP", "TRUNCATE"},
		Message: "destructive statements are not permitted on appdb",
	}})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}

	v := rules.Evaluate(stmt("DROP TABLE customers", inspect.OpDrop, "customers"))
	if !v.Denied {
		t.Fatal("DROP was allowed")
	}
	if v.Message != "destructive statements are not permitted on appdb" {
		t.Errorf("Message = %q; the operator-authored text must reach the user", v.Message)
	}
	if v.Rule != "no-destructive" {
		t.Errorf("Rule = %q, want no-destructive", v.Rule)
	}

	if rules.Evaluate(stmt("SELECT 1", inspect.OpSelect)).Denied {
		t.Error("SELECT was denied")
	}
}

func TestDenyWordsIsCaseInsensitive(t *testing.T) {
	rules, _ := policy.NewRules([]policy.Rule{{
		Name:  "no-drop",
		Type:  policy.MatchDenyWords,
		Words: []string{"drop"},
	}})
	if !rules.Evaluate(stmt("DROP TABLE t", inspect.OpDrop)).Denied {
		t.Error("lowercase rule did not match uppercase SQL")
	}
}

// Operation matching avoids the false positive that makes deny-words blunt:
// the classifier strips literals first, so a DELETE inside a string literal
// never becomes Operation=delete.
func TestOperationBeatsDenyWordsOnLiterals(t *testing.T) {
	text := `SELECT 'DROP TABLE customers' AS warning`

	words, _ := policy.NewRules([]policy.Rule{{
		Name: "words", Type: policy.MatchDenyWords, Words: []string{"DROP"},
	}})
	if !words.Evaluate(stmt(text, inspect.OpSelect)).Denied {
		t.Error("deny-words should (over-)match the literal; test assumption broken")
	}

	ops, _ := policy.NewRules([]policy.Rule{{
		Name: "ops", Type: policy.MatchOperation,
		Operations: []inspect.Operation{inspect.OpDrop},
	}})
	if ops.Evaluate(stmt(text, inspect.OpSelect)).Denied {
		t.Error("operation rule matched a DROP that only appears inside a literal")
	}
}

func TestOperationMatch(t *testing.T) {
	rules, _ := policy.NewRules([]policy.Rule{{
		Name:       "no-writes",
		Type:       policy.MatchOperation,
		Operations: []inspect.Operation{inspect.OpInsert, inspect.OpUpdate, inspect.OpDelete},
		Message:    "this credential is read-only",
	}})

	if !rules.Evaluate(stmt("DELETE FROM t", inspect.OpDelete, "t")).Denied {
		t.Error("DELETE allowed by a no-writes rule")
	}
	if rules.Evaluate(stmt("SELECT 1", inspect.OpSelect)).Denied {
		t.Error("SELECT denied by a no-writes rule")
	}
}

func TestTableMatch(t *testing.T) {
	rules, _ := policy.NewRules([]policy.Rule{{
		Name:    "protect-customers",
		Type:    policy.MatchTable,
		Tables:  []string{"customers"},
		Message: "the customers table is off limits",
	}})

	if !rules.Evaluate(stmt("SELECT * FROM customers", inspect.OpSelect, "customers")).Denied {
		t.Error("bare table name did not match")
	}
	// Schema qualification must still match the bare rule.
	if !rules.Evaluate(stmt("SELECT * FROM public.customers", inspect.OpSelect, "public.customers")).Denied {
		t.Error("schema-qualified name did not match a bare rule")
	}
	if rules.Evaluate(stmt("SELECT * FROM orders", inspect.OpSelect, "orders")).Denied {
		t.Error("unrelated table was denied")
	}
}

// Tables is best-effort. A rule protecting something critical can opt into
// denying when the table list could not be determined, so "we could not tell"
// does not silently read as "safe".
func TestRequireTableMatchFailsClosed(t *testing.T) {
	lenient, _ := policy.NewRules([]policy.Rule{{
		Name: "lenient", Type: policy.MatchTable, Tables: []string{"customers"},
	}})
	if lenient.Evaluate(stmt("SOMETHING UNPARSEABLE", inspect.OpUnknown)).Denied {
		t.Error("lenient rule denied a statement with no extracted tables")
	}

	strict, _ := policy.NewRules([]policy.Rule{{
		Name: "strict", Type: policy.MatchTable, Tables: []string{"customers"},
		RequireTableMatch: true,
		Message:           "cannot verify which tables this statement touches",
	}})
	v := strict.Evaluate(stmt("SOMETHING UNPARSEABLE", inspect.OpUnknown))
	if !v.Denied {
		t.Error("strict rule allowed a statement whose tables are unknown")
	}
	if v.Message != "cannot verify which tables this statement touches" {
		t.Errorf("Message = %q", v.Message)
	}
}

func TestPatternMatch(t *testing.T) {
	rules, _ := policy.NewRules([]policy.Rule{{
		Name:    "no-unbounded-delete",
		Type:    policy.MatchPattern,
		Pattern: `(?i)^\s*DELETE\s+FROM\s+\w+\s*;?\s*$`,
		Message: "DELETE requires a WHERE clause",
	}})

	if !rules.Evaluate(stmt("DELETE FROM customers", inspect.OpDelete, "customers")).Denied {
		t.Error("unbounded DELETE was allowed")
	}
	if rules.Evaluate(stmt("DELETE FROM customers WHERE id = 1", inspect.OpDelete, "customers")).Denied {
		t.Error("bounded DELETE was denied")
	}
}

// A bad config must fail at startup, before the first request that would
// trip it.
func TestInvalidRulesRejectedAtConstruction(t *testing.T) {
	cases := map[string]policy.Rule{
		"bad regex":     {Name: "r", Type: policy.MatchPattern, Pattern: "([unclosed"},
		"no words":      {Name: "r", Type: policy.MatchDenyWords},
		"no operations": {Name: "r", Type: policy.MatchOperation},
		"no tables":     {Name: "r", Type: policy.MatchTable},
		"unknown type":  {Name: "r", Type: "nonsense"},
	}
	for name, rule := range cases {
		if _, err := policy.NewRules([]policy.Rule{rule}); err == nil {
			t.Errorf("%s: NewRules accepted an invalid rule", name)
		}
	}
}

func TestFirstMatchWins(t *testing.T) {
	rules, _ := policy.NewRules([]policy.Rule{
		{Name: "first", Type: policy.MatchOperation,
			Operations: []inspect.Operation{inspect.OpDelete}, Message: "first"},
		{Name: "second", Type: policy.MatchDenyWords,
			Words: []string{"DELETE"}, Message: "second"},
	})
	v := rules.Evaluate(stmt("DELETE FROM t", inspect.OpDelete, "t"))
	if v.Rule != "first" {
		t.Errorf("Rule = %q, want first; order must express precedence", v.Rule)
	}
}

func TestGeneratedMessageWhenNoneConfigured(t *testing.T) {
	rules, _ := policy.NewRules([]policy.Rule{{
		Name: "no-drop", Type: policy.MatchOperation,
		Operations: []inspect.Operation{inspect.OpDrop},
	}})
	v := rules.Evaluate(stmt("DROP TABLE t", inspect.OpDrop, "t"))
	if v.Message == "" {
		t.Error("a denial must always carry a message")
	}
	if !strings.Contains(v.Message, "no-drop") {
		t.Errorf("generated message should name the rule, got %q", v.Message)
	}
}

// --- OPA -----------------------------------------------------------------

func TestOPAAllow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"allow": true}})
	}))
	defer srv.Close()

	c := &policy.OPAClient{URL: srv.URL}
	if v := c.Evaluate(stmt("SELECT 1", inspect.OpSelect)); v.Denied {
		t.Errorf("allow=true produced a denial: %+v", v)
	}
}

func TestOPADenyCarriesMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{
			"allow":   false,
			"message": "service owners forbid DELETE on customers",
			"rule":    "svc-owner-policy",
		}})
	}))
	defer srv.Close()

	c := &policy.OPAClient{URL: srv.URL}
	v := c.Evaluate(stmt("DELETE FROM customers", inspect.OpDelete, "customers"))
	if !v.Denied {
		t.Fatal("allow=false did not deny")
	}
	if v.Message != "service owners forbid DELETE on customers" {
		t.Errorf("Message = %q", v.Message)
	}
	if v.Rule != "svc-owner-policy" {
		t.Errorf("Rule = %q", v.Rule)
	}
}

// A policy written as `deny` rather than `allow` must work without the caller
// adapting.
func TestOPADeniedPolarity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{
			"denied": true, "message": "nope",
		}})
	}))
	defer srv.Close()

	if v := (&policy.OPAClient{URL: srv.URL}).Evaluate(stmt("DROP TABLE t", inspect.OpDrop)); !v.Denied {
		t.Error("denied=true did not deny")
	}
}

// `data.pkg.allow` queried directly returns a bare boolean.
func TestOPABareBooleanResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"result": false})
	}))
	defer srv.Close()

	if v := (&policy.OPAClient{URL: srv.URL}).Evaluate(stmt("DROP TABLE t", inspect.OpDrop)); !v.Denied {
		t.Error("bare false did not deny")
	}
}

// An undefined decision is the absence of an allow, so it denies.
func TestOPAUndefinedResultDenies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	v := (&policy.OPAClient{URL: srv.URL}).Evaluate(stmt("SELECT 1", inspect.OpSelect))
	if !v.Denied {
		t.Error("an undefined OPA decision was treated as an allow")
	}
}

func TestOPAUnreachableFailsClosed(t *testing.T) {
	c := &policy.OPAClient{URL: "http://127.0.0.1:1/never", Timeout: 100 * time.Millisecond}
	v := c.Evaluate(stmt("SELECT 1", inspect.OpSelect))
	if !v.Denied {
		t.Error("an unreachable policy engine must fail closed")
	}
	if v.Err == nil {
		t.Error("Err should record why the evaluation failed")
	}
}

func TestOPAUnreachableFailOpen(t *testing.T) {
	c := &policy.OPAClient{URL: "http://127.0.0.1:1/never", Timeout: 100 * time.Millisecond, FailOpen: true}
	v := c.Evaluate(stmt("SELECT 1", inspect.OpSelect))
	if v.Denied {
		t.Error("FailOpen did not allow")
	}
	if v.Err == nil {
		t.Error("FailOpen must still record the error")
	}
}

// The input document is a public contract with whoever writes the Rego.
// Renaming a field silently breaks their policy, so this test pins the shape.
func TestOPAInputDocumentShape(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"allow": true}})
	}))
	defer srv.Close()

	c := &policy.OPAClient{URL: srv.URL, Context: map[string]string{"user": "alice"}}
	s := inspect.Statement{
		Protocol:  inspect.Postgres,
		Direction: inspect.FromClient,
		Text:      "DELETE FROM customers WHERE id = 1",
		Operation: inspect.OpDelete,
		Tables:    []string{"customers"},
		Database:  "appdb",
		Metadata:  map[string]string{"pg.message": "Query"},
	}
	c.Evaluate(s)

	input, ok := got["input"].(map[string]any)
	if !ok {
		t.Fatalf("request body has no input object: %+v", got)
	}
	for key, want := range map[string]any{
		"protocol":  "postgres",
		"direction": "client",
		"statement": "DELETE FROM customers WHERE id = 1",
		"operation": "delete",
		"database":  "appdb",
	} {
		if input[key] != want {
			t.Errorf("input[%q] = %v, want %v", key, input[key], want)
		}
	}
	tables, _ := input["tables"].([]any)
	if len(tables) != 1 || tables[0] != "customers" {
		t.Errorf("input[tables] = %v, want [customers]", input["tables"])
	}
	ctx, _ := input["context"].(map[string]any)
	if ctx["user"] != "alice" {
		t.Errorf("input[context][user] = %v, want alice", ctx["user"])
	}
}

func TestOPAHTTPErrorFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if v := (&policy.OPAClient{URL: srv.URL}).Evaluate(stmt("SELECT 1", inspect.OpSelect)); !v.Denied {
		t.Error("a 500 from OPA was treated as an allow")
	}
}

// --- chain ---------------------------------------------------------------

// Local rules run first, so a statement a local rule already forbids never
// costs a network round-trip.
func TestChainShortCircuitsBeforeOPA(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"allow": true}})
	}))
	defer srv.Close()

	local, _ := policy.NewRules([]policy.Rule{{
		Name: "no-drop", Type: policy.MatchOperation,
		Operations: []inspect.Operation{inspect.OpDrop},
		Message:    "no",
	}})
	chain := policy.Chain{local, &policy.OPAClient{URL: srv.URL}}

	if v := chain.Evaluate(stmt("DROP TABLE t", inspect.OpDrop, "t")); !v.Denied {
		t.Fatal("chain allowed a DROP")
	}
	if called {
		t.Error("OPA was called even though a local rule already denied")
	}
}

func TestChainReachesOPAWhenLocalAllows(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"allow": true}})
	}))
	defer srv.Close()

	local, _ := policy.NewRules([]policy.Rule{{
		Name: "no-drop", Type: policy.MatchOperation,
		Operations: []inspect.Operation{inspect.OpDrop},
	}})
	chain := policy.Chain{local, &policy.OPAClient{URL: srv.URL}}

	if v := chain.Evaluate(stmt("SELECT 1", inspect.OpSelect)); v.Denied {
		t.Fatalf("chain denied a SELECT: %+v", v)
	}
	if !called {
		t.Error("OPA was not consulted after the local rules allowed")
	}
}

func TestEmptyChainAllows(t *testing.T) {
	// Go parses a composite literal in an `if` condition as the block
	// opener, hence the variable.
	var empty policy.Chain
	if empty.Evaluate(stmt("DROP TABLE t", inspect.OpDrop)).Denied {
		t.Error("an empty chain denied")
	}
}

// stubEvaluator returns a fixed verdict and records that it ran.
type stubEvaluator struct {
	verdict policy.Verdict
	ran     bool
}

func (s *stubEvaluator) Evaluate(inspect.Statement) policy.Verdict {
	s.ran = true
	return s.verdict
}

// The bug: the chain stopped on any non-nil Err, so a fail-open evaluator --
// which reports Denied=false with Err set, exactly as OPAClient.failure and
// Rules.Evaluate do -- silently disabled every evaluator behind it. One
// unreachable OPA or one uncompilable regex would turn the rest of the
// policy off, which is the opposite of what fail-open asks for.
func TestChainContinuesPastAFailOpenError(t *testing.T) {
	degraded := &stubEvaluator{verdict: policy.Verdict{Err: errors.New("opa unreachable")}}
	strict := &stubEvaluator{verdict: policy.Deny("no-drop", "no drops here")}
	chain := policy.Chain{degraded, strict}

	v := chain.Evaluate(stmt("DROP TABLE t", inspect.OpDrop, "t"))
	if !strict.ran {
		t.Fatal("a fail-open error stopped the chain; the later evaluator never ran")
	}
	if !v.Denied {
		t.Fatal("the DROP was allowed despite a later evaluator denying it")
	}
	if v.Rule != "no-drop" || v.Message != "no drops here" {
		t.Errorf("verdict lost the denying rule: %+v", v)
	}
	// The denial carries the earlier failure too: an operator needs to know
	// their first evaluator was degraded, even though the second caught this
	// statement anyway.
	if v.Err == nil || !strings.Contains(v.Err.Error(), "opa unreachable") {
		t.Errorf("Err = %v, want the accumulated fail-open error", v.Err)
	}
}

// With no denial anywhere, the chain allows and hands back every error it
// collected, so gate.inspect can audit them.
func TestChainAccumulatesErrorsWhenNothingDenies(t *testing.T) {
	first := &stubEvaluator{verdict: policy.Verdict{Err: errors.New("regex did not compile")}}
	second := &stubEvaluator{verdict: policy.Verdict{Err: errors.New("opa unreachable")}}
	third := &stubEvaluator{verdict: policy.Allow()}

	v := policy.Chain{first, second, third}.Evaluate(stmt("SELECT 1", inspect.OpSelect))
	if v.Denied {
		t.Fatal("the chain denied although no evaluator did")
	}
	if !third.ran {
		t.Error("the last evaluator never ran")
	}
	for _, want := range []string{"regex did not compile", "opa unreachable"} {
		if v.Err == nil || !strings.Contains(v.Err.Error(), want) {
			t.Errorf("Err = %v, want it to mention %q", v.Err, want)
		}
	}
}

// A clean run must not invent an error. gate.inspect logs a warning on any
// non-nil Err, so a spurious one is noise on every statement.
func TestChainReportsNoErrorWhenEveryEvaluatorIsHealthy(t *testing.T) {
	chain := policy.Chain{&stubEvaluator{verdict: policy.Allow()}, &stubEvaluator{verdict: policy.Allow()}}

	if v := chain.Evaluate(stmt("SELECT 1", inspect.OpSelect)); v.Err != nil {
		t.Errorf("Err = %v on a healthy chain", v.Err)
	}
}

// Fail-closed is unchanged: an evaluator that turns its error into a denial
// still stops the chain, because the denial does.
func TestChainStopsOnAFailClosedError(t *testing.T) {
	closed := &stubEvaluator{verdict: policy.Verdict{
		Denied: true, Message: "policy engine unavailable; denying",
		Rule: "opa", Err: errors.New("opa unreachable"),
	}}
	later := &stubEvaluator{verdict: policy.Allow()}

	v := policy.Chain{closed, later}.Evaluate(stmt("SELECT 1", inspect.OpSelect))
	if !v.Denied {
		t.Fatal("a fail-closed error did not deny")
	}
	if later.ran {
		t.Error("evaluation continued past a denial")
	}
	if v.Err == nil {
		t.Error("the denial dropped its cause")
	}
}

// --- annotation merge -------------------------------------------------------

// annotator is an evaluator that only contributes annotations.
type annotator struct {
	notes  map[string]string
	denied bool
}

func (a annotator) Evaluate(inspect.Statement) policy.Verdict {
	v := policy.Verdict{Denied: a.denied}
	if a.denied {
		v.Rule, v.Message = "annotator", "denied"
	}
	v.Annotations = a.notes
	return v
}

func risk(level, action string) map[string]string {
	return map[string]string{
		policy.AnnotationRiskLevel:  level,
		policy.AnnotationRiskAction: action,
	}
}

// A lane may carry several ai_analysis rules, each its own evaluator emitting
// the same two keys. Last-write-wins would let a rule that rated a statement
// low erase one that rated it high, and the audit record carries a single
// risk_level that the session rollup keeps the maximum of — so the downgrade
// would understate the whole session.
func TestChainKeepsHighestRisk(t *testing.T) {
	for _, tc := range []struct {
		name       string
		first      map[string]string
		second     map[string]string
		wantLevel  string
		wantAction string
	}{
		{"high then low", risk("high", "block"), risk("low", "allow"), "high", "block"},
		{"low then high", risk("low", "allow"), risk("high", "block"), "high", "block"},
		{"medium then low", risk("medium", "warn"), risk("low", "allow"), "medium", "warn"},
		{"low then medium", risk("low", "allow"), risk("medium", "warn"), "medium", "warn"},
		{"equal keeps the first", risk("high", "block"), risk("high", "warn"), "high", "block"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chain := policy.Chain{
				annotator{notes: tc.first},
				annotator{notes: tc.second},
			}
			v := chain.Evaluate(inspect.Statement{})
			if got := v.Annotations[policy.AnnotationRiskLevel]; got != tc.wantLevel {
				t.Errorf("risk_level = %q, want %q", got, tc.wantLevel)
			}
			if got := v.Annotations[policy.AnnotationRiskAction]; got != tc.wantAction {
				t.Errorf("risk_action = %q, want %q", got, tc.wantAction)
			}
		})
	}
}

// The level and its action travel together. Merging the two keys
// independently yields {high, allow} from a high->warn rule and a low->allow
// one, describing a mapping no rule configured.
func TestChainKeepsRiskPairConsistent(t *testing.T) {
	chain := policy.Chain{
		annotator{notes: risk("high", "warn")},
		annotator{notes: risk("low", "allow")},
	}
	v := chain.Evaluate(inspect.Statement{})

	if got := v.Annotations[policy.AnnotationRiskAction]; got != "warn" {
		t.Errorf("risk_action = %q, want warn: the low rule's action was paired "+
			"with the high rule's level", got)
	}
}

// A denial short-circuits, so the annotations gathered before it must still
// reach the record: that is how a blocked statement keeps the risk an earlier
// rule established.
func TestChainCarriesAnnotationsThroughDenial(t *testing.T) {
	chain := policy.Chain{
		annotator{notes: risk("high", "warn")},
		annotator{notes: map[string]string{policy.AnnotationRiskAction: "block"}, denied: true},
	}
	v := chain.Evaluate(inspect.Statement{})

	if !v.Denied {
		t.Fatal("the chain did not deny")
	}
	if got := v.Annotations[policy.AnnotationRiskLevel]; got != "high" {
		t.Errorf("risk_level = %q, want high preserved through the denial", got)
	}
	// The refuse path denies without classifying, so its action is the last
	// word on what happened.
	if got := v.Annotations[policy.AnnotationRiskAction]; got != "block" {
		t.Errorf("risk_action = %q, want block", got)
	}
}

// A level arriving with no action must not sit beside a previous rule's
// action, which would misreport what was done.
func TestChainDropsStaleActionWhenLevelWins(t *testing.T) {
	chain := policy.Chain{
		annotator{notes: risk("low", "allow")},
		annotator{notes: map[string]string{policy.AnnotationRiskLevel: "high"}},
	}
	v := chain.Evaluate(inspect.Statement{})

	if got := v.Annotations[policy.AnnotationRiskLevel]; got != "high" {
		t.Errorf("risk_level = %q, want high", got)
	}
	if got, ok := v.Annotations[policy.AnnotationRiskAction]; ok {
		t.Errorf("risk_action = %q, want it dropped: it belonged to the low verdict", got)
	}
}

// Keys outside the risk pair keep last-write-wins.
func TestChainMergesOtherKeysLastWins(t *testing.T) {
	chain := policy.Chain{
		annotator{notes: map[string]string{"other": "first"}},
		annotator{notes: map[string]string{"other": "second"}},
	}
	v := chain.Evaluate(inspect.Statement{})
	if got := v.Annotations["other"]; got != "second" {
		t.Errorf("other = %q, want second", got)
	}
}

// An unrecognized level must not displace a real one.
func TestChainIgnoresUnknownRiskLevel(t *testing.T) {
	chain := policy.Chain{
		annotator{notes: risk("high", "block")},
		annotator{notes: risk("catastrophic", "allow")},
	}
	v := chain.Evaluate(inspect.Statement{})
	if got := v.Annotations[policy.AnnotationRiskLevel]; got != "high" {
		t.Errorf("risk_level = %q, want high", got)
	}
}
