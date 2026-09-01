package policy_test

import (
	"errors"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// The whole point of observe mode. Before it existed, the off-switch built no
// evaluator at all and the audit record carried an empty rule and an empty
// message, so a week of running that way answered nothing. A dry run has to
// let the statement through AND say which rule objected.
func TestObserveTurnsADenialIntoAnAllowThatNamesTheRule(t *testing.T) {
	inner := &stubEvaluator{verdict: policy.Deny("no-drop", "schema changes go through migrations")}
	obs := policy.Observe{Evaluator: inner}

	v := obs.Evaluate(stmt("DROP TABLE customers", inspect.OpDrop, "customers"))

	if v.Denied {
		t.Fatal("observe mode denied")
	}
	if v.Rule != "no-drop" {
		t.Errorf("Rule = %q, want the rule that would have denied", v.Rule)
	}
	if v.Message != "schema changes go through migrations" {
		t.Errorf("Message = %q, want the operator's message preserved", v.Message)
	}
	if got := v.Annotations[policy.AnnotationWouldDeny]; got != "no-drop" {
		t.Errorf("%s = %q, want the rule name", policy.AnnotationWouldDeny, got)
	}
}

// Err records why an evaluation broke, and a degraded evaluator is worth the
// same audit line in a dry run as in enforcement. Losing it here would make
// observe mode the one place an OPA outage goes unrecorded.
func TestObserveKeepsTheEvaluationError(t *testing.T) {
	boom := errors.New("opa unreachable")
	inner := &stubEvaluator{verdict: policy.Verdict{
		Denied: true, Rule: "opa", Message: "policy engine unreachable; denying", Err: boom,
	}}

	v := policy.Observe{Evaluator: inner}.Evaluate(stmt("SELECT 1", inspect.OpSelect))

	if v.Denied {
		t.Fatal("observe mode denied")
	}
	if !errors.Is(v.Err, boom) {
		t.Errorf("Err = %v, want the inner failure", v.Err)
	}
}

// An allowed statement must look untouched. Stamping would_deny on everything
// would make the annotation useless for counting what enforcement will cost.
func TestObservePassesAnAllowThrough(t *testing.T) {
	inner := &stubEvaluator{verdict: policy.Verdict{
		Annotations: map[string]string{policy.AnnotationRiskLevel: "low"},
	}}

	v := policy.Observe{Evaluator: inner}.Evaluate(stmt("SELECT 1", inspect.OpSelect))

	if v.Denied {
		t.Fatal("observe mode denied an allow")
	}
	if _, ok := v.Annotations[policy.AnnotationWouldDeny]; ok {
		t.Errorf("an allowed statement carried %s", policy.AnnotationWouldDeny)
	}
	if v.Annotations[policy.AnnotationRiskLevel] != "low" {
		t.Errorf("Annotations = %v, want the inner annotations untouched", v.Annotations)
	}
}

// The inner evaluator's annotation map may outlive its verdict: Chain returns
// the EvalContext's own map, and that context is reused for the rest of the
// statement's evaluation. Writing into it would leak one statement's
// would_deny onto the audit record of the statements after it.
func TestObserveDoesNotWriteIntoTheInnerAnnotationMap(t *testing.T) {
	shared := map[string]string{policy.AnnotationRiskLevel: "high"}
	inner := &stubEvaluator{verdict: policy.Verdict{
		Denied: true, Rule: "no-drop", Annotations: shared,
	}}

	v := policy.Observe{Evaluator: inner}.Evaluate(stmt("DROP TABLE t", inspect.OpDrop, "t"))

	if _, ok := shared[policy.AnnotationWouldDeny]; ok {
		t.Error("observe mutated the inner evaluator's annotation map")
	}
	if v.Annotations[policy.AnnotationRiskLevel] != "high" {
		t.Errorf("Annotations = %v, want the inner annotations copied through", v.Annotations)
	}
}

// recordingContextual is a ContextualEvaluator that reports which of its two
// methods the caller reached, and what context arrived with it.
type recordingContextual struct {
	verdict policy.Verdict

	withCalled  bool
	plainCalled bool
	seenContext map[string]string
}

func (r *recordingContextual) Evaluate(inspect.Statement) policy.Verdict {
	r.plainCalled = true
	return r.verdict
}

func (r *recordingContextual) EvaluateWith(_ inspect.Statement, ec *policy.EvalContext) policy.Verdict {
	r.withCalled = true
	if ec != nil {
		r.seenContext = ec.Context
	}
	return r.verdict
}

// Observe has to implement ContextualEvaluator, not just Evaluator.
// Gate.evaluate type-asserts for the interface before seeding the session
// facts, and Chain does the same for every evaluator it holds. A wrapper that
// only carried Evaluate would pass this test's sibling above and still empty
// input.context on every OPA call the lane makes.
func TestObserveForwardsTheEvalContextToAContextualInner(t *testing.T) {
	inner := &recordingContextual{verdict: policy.Deny("no-drop", "no drops")}

	var obs policy.Evaluator = policy.Observe{Evaluator: inner}
	ce, ok := obs.(policy.ContextualEvaluator)
	if !ok {
		t.Fatal("Observe does not implement ContextualEvaluator; the gate would drop the session facts")
	}

	ec := &policy.EvalContext{Context: map[string]string{"principal": "ana@corp"}}
	v := ce.EvaluateWith(stmt("DROP TABLE t", inspect.OpDrop, "t"), ec)

	if !inner.withCalled {
		t.Error("Observe did not call the inner evaluator's EvaluateWith")
	}
	if inner.plainCalled {
		t.Error("Observe called Evaluate on an evaluator that has EvaluateWith")
	}
	if inner.seenContext["principal"] != "ana@corp" {
		t.Errorf("inner saw context %v, want the session facts", inner.seenContext)
	}
	if v.Denied || v.Annotations[policy.AnnotationWouldDeny] != "no-drop" {
		t.Errorf("EvaluateWith did not observe the denial: %+v", v)
	}
}

// An evaluator with no EvaluateWith still has to run. Observe falls back
// rather than refusing, exactly as Chain does.
func TestObserveFallsBackToEvaluateOnAPlainInner(t *testing.T) {
	inner := &stubEvaluator{verdict: policy.Deny("no-drop", "no drops")}

	v := policy.Observe{Evaluator: inner}.EvaluateWith(
		stmt("DROP TABLE t", inspect.OpDrop, "t"), &policy.EvalContext{})

	if !inner.ran {
		t.Error("the inner evaluator did not run")
	}
	if v.Denied || v.Rule != "no-drop" {
		t.Errorf("verdict = %+v, want an allow naming the rule", v)
	}
}

// A lane whose chain resolved to nothing gets a nil evaluator, and observe is
// the mode most likely to be set on a lane with no rules yet. Panicking on it
// would take the lane's traffic down.
func TestObserveWithNoInnerAllows(t *testing.T) {
	var obs policy.Observe
	if obs.Evaluate(stmt("DROP TABLE t", inspect.OpDrop, "t")).Denied {
		t.Error("an empty Observe denied")
	}
	if obs.EvaluateWith(stmt("DROP TABLE t", inspect.OpDrop, "t"), &policy.EvalContext{}).Denied {
		t.Error("an empty Observe denied through EvaluateWith")
	}
}

// Observe wraps a chain in practice, so the composition has to hold: the
// chain's own annotations survive and the denial it produced is reported
// rather than enforced.
func TestObserveOverAChain(t *testing.T) {
	rules, err := policy.NewRules([]policy.Rule{{
		Name: "no-drop", Type: policy.MatchOperation,
		Operations: []inspect.Operation{inspect.OpDrop},
		Message:    "schema changes go through migrations",
	}})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}
	classifier := &stubEvaluator{verdict: policy.Verdict{
		Annotations: map[string]string{policy.AnnotationRiskLevel: "high"},
	}}

	obs := policy.Observe{Evaluator: policy.Chain{classifier, rules}}
	v := obs.Evaluate(stmt("DROP TABLE customers", inspect.OpDrop, "customers"))

	if v.Denied {
		t.Fatal("observe mode denied")
	}
	if v.Annotations[policy.AnnotationWouldDeny] != "no-drop" {
		t.Errorf("%s = %q, want no-drop", policy.AnnotationWouldDeny, v.Annotations[policy.AnnotationWouldDeny])
	}
	if v.Annotations[policy.AnnotationRiskLevel] != "high" {
		t.Errorf("the chain's own annotations were lost: %v", v.Annotations)
	}
}
