package daemon

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/policy"
)

// stubDeps supplies buildPolicy with an analyzer that needs no credential
// and no network, so a test can assert on chain SHAPE.
func stubDeps() *analyzerDeps {
	return &analyzerDeps{
		cfg:      &AnalyzerConfig{Provider: "stub", Model: "m"},
		provider: stubAnalyzerProvider{},
	}
}

// phases reports the Phase of every OPA client in a built chain, in order.
func phases(t *testing.T, pol policy.Evaluator) []policy.Phase {
	t.Helper()
	chain, ok := pol.(policy.Chain)
	if !ok {
		t.Fatalf("buildPolicy returned %T, want policy.Chain", pol)
	}
	var out []policy.Phase
	for _, e := range chain {
		if c, isOPA := e.(*policy.OPAClient); isOPA {
			out = append(out, c.Phase)
		}
	}
	return out
}

// deferRule is an ai_analysis rule that hands high risk to a policy.
func deferRule(name string) policy.Rule {
	r := aiRule(name)
	r.HighRisk = "defer"
	return r
}

// denyWordsRule is a local rule, the producer half of what deferRule covers:
// the same request spelled through action rather than per risk level.
func denyWordsRule(name, action string) policy.Rule {
	return policy.Rule{
		Name:   name,
		Type:   policy.MatchDenyWords,
		Words:  []string{"DELETE"},
		Action: action,
	}
}

// lanePolicy is an enforcing lane policy, whatever produces on it.
func lanePolicy(opa *OPAConfig, rules ...policy.Rule) PolicyConfig {
	return PolicyConfig{Enforce: new(true), OPA: opa, Rules: rules}
}

// A lane with no defer and no gate must build exactly the chain it built
// before this feature existed, phase and all.
func TestSinglePhaseChainIsUnchanged(t *testing.T) {
	pc := lanePolicy(&OPAConfig{URL: "http://opa:8181/v1/data/hoop"}, aiRule("risky"))

	pol, err := buildPolicy(pc, nil, stubDeps())
	if err != nil {
		t.Fatalf("buildPolicy: %v", err)
	}
	if got := phases(t, pol); len(got) != 1 || got[0] != "" {
		t.Errorf("phases = %v, want one unphased call", got)
	}
	chain := pol.(policy.Chain)
	if _, isOPA := chain[0].(*policy.OPAClient); !isOPA {
		t.Errorf("chain[0] = %T, want OPA before the analyzer", chain[0])
	}
}

// A deferred level has to reach something that can act on it, and the only
// evaluator that can is a decision placed after the analyzer.
func TestDeferPutsOPAAfterTheAnalyzer(t *testing.T) {
	pc := lanePolicy(&OPAConfig{URL: "http://opa:8181/v1/data/hoop"}, deferRule("risky"))

	pol, err := buildPolicy(pc, nil, stubDeps())
	if err != nil {
		t.Fatalf("buildPolicy: %v", err)
	}
	if got := phases(t, pol); len(got) != 1 || got[0] != policy.PhaseDecide {
		t.Fatalf("phases = %v, want a single decide phase", got)
	}
	chain := pol.(policy.Chain)
	if _, isOPA := chain[len(chain)-1].(*policy.OPAClient); !isOPA {
		t.Errorf("chain ends with %T, want the decide phase last", chain[len(chain)-1])
	}
}

// The gate is how the cost control survives deferring: OPA on both sides, so
// a statement Rego refuses for free never reaches a paid classifier.
func TestGateWrapsTheAnalyzer(t *testing.T) {
	pc := lanePolicy(&OPAConfig{URL: "http://opa:8181/v1/data/hoop", Gate: true}, aiRule("risky"))

	pol, err := buildPolicy(pc, nil, stubDeps())
	if err != nil {
		t.Fatalf("buildPolicy: %v", err)
	}
	got := phases(t, pol)
	if len(got) != 2 || got[0] != policy.PhaseGate || got[1] != policy.PhaseDecide {
		t.Fatalf("phases = %v, want gate then decide", got)
	}
	chain := pol.(policy.Chain)
	if _, isOPA := chain[0].(*policy.OPAClient); !isOPA {
		t.Errorf("chain[0] = %T, want the gate first", chain[0])
	}
	if _, isOPA := chain[len(chain)-1].(*policy.OPAClient); !isOPA {
		t.Errorf("chain ends with %T, want the decision last", chain[len(chain)-1])
	}
}

// Deferring to a decision that does not exist allows everything, which is the
// opposite of what the operator asked for.
func TestDeferWithoutOPAIsRefused(t *testing.T) {
	cfg := pgLane(deferRule("risky"))
	cfg.Analyzer = &AnalyzerConfig{Provider: "stub", Model: "m"}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("a rule deferring to nothing was accepted")
	}
	if !strings.Contains(err.Error(), "defer") {
		t.Errorf("the error does not name the action: %v", err)
	}
}

// A gate answers "is this worth a model call" for an analyzer that is not
// there. It would cost a round trip per statement and change nothing.
func TestGateWithoutAIRulesIsRefused(t *testing.T) {
	cfg := pgLane()
	cfg.Listeners[0].Policy.OPA = &OPAConfig{URL: "http://opa:8181/v1/data/hoop", Gate: true}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("a gate over no ai_analysis rules was accepted")
	}
	if !strings.Contains(err.Error(), "gate") {
		t.Errorf("the error does not name the gate: %v", err)
	}
}

// An empty trigger is a broken rule on an ungated lane and the correct
// configuration on a gated one, where the policy decides what gets analyzed.
func TestEmptyTriggerIsAllowedOnlyWhenGated(t *testing.T) {
	build := func(gate bool) error {
		r := aiRule("risky")
		r.Trigger = nil
		cfg := pgLane(r)
		cfg.Analyzer = &AnalyzerConfig{Provider: "stub", Model: "m"}
		cfg.Listeners[0].Policy.OPA = &OPAConfig{
			URL: "http://opa:8181/v1/data/hoop", Gate: gate,
		}
		return cfg.Validate()
	}

	if err := build(false); err == nil {
		t.Error("an untriggered rule on an ungated lane was accepted")
	}
	if err := build(true); err != nil {
		t.Errorf("an untriggered rule on a gated lane was refused: %v", err)
	}
}

// require_review still has no backend, and the message must point at the
// alternative that now exists rather than only at block and warn.
func TestRequireReviewNamesDeferAsTheAlternative(t *testing.T) {
	r := aiRule("risky")
	r.HighRisk = "require_review"
	cfg := pgLane(r)
	cfg.Analyzer = &AnalyzerConfig{Provider: "stub", Model: "m"}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("require_review was accepted")
	}
	if !strings.Contains(err.Error(), "defer") {
		t.Errorf("the error does not offer defer: %v", err)
	}
}

// A local rule that defers asks for the same thing an ai rule asks for, so
// anyDeferred reads the whole rule set. A lane with no ai_analysis rule at
// all still needs a decision after its producer, or the finding reaches
// nothing and the rule matches and then allows.
func TestLocalDeferMakesTheLaneTwoPhase(t *testing.T) {
	pc := lanePolicy(
		&OPAConfig{URL: "http://opa:8181/v1/data/hoop"},
		denyWordsRule("no-destructive", policy.ActionDefer),
	)

	pol, err := buildPolicy(pc, nil, stubDeps())
	if err != nil {
		t.Fatalf("buildPolicy: %v", err)
	}
	if got := phases(t, pol); len(got) != 1 || got[0] != policy.PhaseDecide {
		t.Fatalf("phases = %v, want a single decide phase", got)
	}
	chain := pol.(policy.Chain)
	if len(chain) != 2 {
		t.Fatalf("chain has %d evaluators, want [rules, opa(decide)]", len(chain))
	}
	if _, isRules := chain[0].(*policy.Rules); !isRules {
		t.Errorf("chain[0] = %T, want the local rules first", chain[0])
	}
	if _, isOPA := chain[1].(*policy.OPAClient); !isOPA {
		t.Errorf("chain[1] = %T, want the decide phase last", chain[1])
	}

	// The same lane with the action dropped is the old single-call shape.
	// Without this arm the assertions above pass on any two-element chain.
	pc.Rules = []policy.Rule{denyWordsRule("no-destructive", "")}
	pol, err = buildPolicy(pc, nil, stubDeps())
	if err != nil {
		t.Fatalf("buildPolicy without the action: %v", err)
	}
	if got := phases(t, pol); len(got) != 1 || got[0] != "" {
		t.Errorf("phases = %v, want one unphased call when nothing defers", got)
	}
}

// Deferring a local match to a lane with no policy.opa.url writes a finding
// nobody reads: the rule matches, allows, and looks like enforcement.
func TestLocalDeferWithoutOPAIsRefused(t *testing.T) {
	err := pgLane(denyWordsRule("no-destructive", policy.ActionDefer)).Validate()
	if err == nil {
		t.Fatal("a local rule deferring to nothing was accepted")
	}
	if !strings.Contains(err.Error(), "no-destructive") ||
		!strings.Contains(err.Error(), "policy.opa.url") {
		t.Errorf("the error does not name the rule and what it lacks: %v", err)
	}
}

// An unknown action decodes into a rule that denies, so `action: warn` would
// block what the operator meant to let through with a warning.
func TestUnknownLocalActionIsRefused(t *testing.T) {
	err := pgLane(denyWordsRule("no-destructive", "warn")).Validate()
	if err == nil {
		t.Fatal("an unknown action was accepted")
	}
	if !strings.Contains(err.Error(), "unknown action") ||
		!strings.Contains(err.Error(), "no-destructive") {
		t.Errorf("the error does not name the bad action and its rule: %v", err)
	}
}

// An `action:` on an ai_analysis rule is read by nobody: splitAnalyzerRules
// lifts these out before policy.newRules sees them, so the refusal there is
// unreachable from a config file. Accepting the field leaves an operator
// believing they deferred a rule that is still deciding for itself.
func TestActionOnAIRuleIsRefused(t *testing.T) {
	r := aiRule("risky")
	r.Action = policy.ActionDefer
	cfg := pgLane(r)
	cfg.Analyzer = &AnalyzerConfig{Provider: "stub", Model: "m"}
	cfg.Listeners[0].Policy.OPA = &OPAConfig{URL: "http://opa:8181/v1/data/x"}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("action: defer on an ai_analysis rule was accepted and ignored")
	}
	for _, want := range []string{"risky", "high"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}
