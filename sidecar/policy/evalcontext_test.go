package policy_test

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// aiSource is the analyzer's findings key, spelled literally on purpose:
// policy does not know its producers, and importing the analyzer here to
// borrow the constant would invert that.
const aiSource = "ai_analysis"

// capturingOPA serves a fixed result and records the input document every
// call carried, which is the contract this feature adds.
func capturingOPA(t *testing.T, result string) (*httptest.Server, func() map[string]any) {
	t.Helper()
	var last map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input map[string]any `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		last = body.Input
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(result)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() map[string]any { return last }
}

// producedContext builds the context the producers on a lane would have left
// behind by the time the decide phase runs.
func producedContext(found ...policy.Finding) *policy.EvalContext {
	ec := &policy.EvalContext{}
	for _, f := range found {
		ec.AddFinding(f)
	}
	return ec
}

// findingIn digs one source out of the captured input document.
func findingIn(t *testing.T, input map[string]any, source string) map[string]any {
	t.Helper()
	found, ok := input["findings"].(map[string]any)
	if !ok {
		t.Fatalf("input carried no findings object: %v", input)
	}
	f, ok := found[source].(map[string]any)
	if !ok {
		t.Fatalf("input.findings carried no %q: %v", source, found)
	}
	return f
}

// The whole point of the channel: what a producer established reaches the
// Rego policy as input.findings, so the block decision is the policy's.
func TestDecidePhaseSendsWhatProducersFound(t *testing.T) {
	srv, input := capturingOPA(t, `{"result":{"denied":true,"message":"high risk needs an approver"}}`)
	c := &policy.OPAClient{URL: srv.URL, Phase: policy.PhaseDecide}

	v := c.EvaluateWith(stmt("DELETE FROM customers", inspect.OpDelete, "customers"), producedContext(
		policy.Finding{
			Source: string(policy.MatchPII), Rule: "no-cpf", Status: policy.FindingOK,
			Values: map[string]any{"entities": []string{"BR_CPF"}},
		},
		policy.Finding{
			Source: aiSource, Rule: "risky-writes", Status: policy.FindingOK,
			Values: map[string]any{"risk_level": "high"},
		},
	))

	if !v.Denied {
		t.Fatalf("the policy denied and the verdict did not: %+v", v)
	}
	if v.Message != "high risk needs an approver" {
		t.Errorf("Message = %q; the policy's message must reach the user", v.Message)
	}
	if got, _ := input()["phase"].(string); got != "decide" {
		t.Errorf("input.phase = %q, want decide", got)
	}

	pii := findingIn(t, input(), string(policy.MatchPII))
	if got, _ := pii["status"].(string); got != "ok" {
		t.Errorf("input.findings.pii.status = %q, want ok", got)
	}
	if got, _ := pii["rule"].(string); got != "no-cpf" {
		t.Errorf("input.findings.pii.rule = %q, want no-cpf", got)
	}
	values, _ := pii["values"].(map[string]any)
	entities, _ := values["entities"].([]any)
	if len(entities) != 1 || entities[0] != "BR_CPF" {
		t.Errorf("input.findings.pii.values.entities = %v, want [BR_CPF]", entities)
	}

	ai := findingIn(t, input(), aiSource)
	aiValues, _ := ai["values"].(map[string]any)
	if got, _ := aiValues["risk_level"].(string); got != "high" {
		t.Errorf("input.findings.ai_analysis.values.risk_level = %q, want high", got)
	}
}

// A producer that ran and could not answer must still appear, carrying its
// status and no values. An absent key would leave a policy unable to tell
// "the scanner found nothing" from "the scanner never ran", which is the
// silent fail-open the status field exists to prevent.
func TestDegradedFindingTravelsWithNoValues(t *testing.T) {
	for _, tc := range []struct{ status, reason string }{
		{policy.FindingError, "provider timed out"},
		{policy.FindingSkipped, ""},
		{policy.FindingUnavailable, "budget_exhausted"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			srv, input := capturingOPA(t, `{"result":{"allow":true}}`)
			c := &policy.OPAClient{URL: srv.URL, Phase: policy.PhaseDecide}

			c.EvaluateWith(stmt("DELETE FROM t", inspect.OpDelete, "t"), producedContext(
				policy.Finding{Source: aiSource, Status: tc.status, Reason: tc.reason},
			))

			ai := findingIn(t, input(), aiSource)
			if got, _ := ai["status"].(string); got != tc.status {
				t.Errorf("input.findings.ai_analysis.status = %q, want %q", got, tc.status)
			}
			if _, present := ai["values"]; present {
				t.Errorf("a producer that did not answer sent values: %v", ai)
			}
			if got, _ := ai["reason"].(string); got != tc.reason {
				t.Errorf("input.findings.ai_analysis.reason = %q, want %q", got, tc.reason)
			}
		})
	}
}

// A lane where nothing reported must produce no input.findings at all, or
// every policy has to tell "no producer configured" apart from "every
// producer failed".
func TestNoProducerMeansNoFindingsField(t *testing.T) {
	srv, input := capturingOPA(t, `{"result":{"allow":true}}`)
	c := &policy.OPAClient{URL: srv.URL, Phase: policy.PhaseDecide}

	c.EvaluateWith(stmt("SELECT 1", inspect.OpSelect), producedContext())

	if _, present := input()["findings"]; present {
		t.Errorf("input carried findings with no producer on the lane: %v", input())
	}
}

// Findings ride the decide phase only. The gate runs BEFORE the producers it
// gates, so sending a local rule's finding there would let a Rego author key
// a gate decision on a channel that is empty for every expensive producer.
func TestGatePhaseCarriesNoFindings(t *testing.T) {
	srv, input := capturingOPA(t, `{"result":{"allow":true}}`)
	c := &policy.OPAClient{URL: srv.URL, Phase: policy.PhaseGate}

	c.EvaluateWith(stmt("DELETE FROM t", inspect.OpDelete, "t"), producedContext(
		policy.Finding{Source: string(policy.MatchDenyWords), Status: policy.FindingOK},
	))

	if _, present := input()["findings"]; present {
		t.Errorf("the gate phase carried findings: %v", input())
	}
	if got, _ := input()["phase"].(string); got != "gate" {
		t.Errorf("input.phase = %q, want gate", got)
	}
}

// A single-call lane must send exactly what it sent before producers could
// report, or every deployed Rego policy sees a changed input document.
func TestSinglePhaseInputIsUnchanged(t *testing.T) {
	srv, input := capturingOPA(t, `{"result":{"allow":true}}`)
	c := &policy.OPAClient{URL: srv.URL}

	c.Evaluate(stmt("SELECT 1", inspect.OpSelect))

	for _, key := range []string{"phase", "findings"} {
		if _, present := input()[key]; present {
			t.Errorf("a single-phase call sent %q: %v", key, input())
		}
	}
}

// The gate's answer travels back through the chain, not through the verdict:
// "spend a model call on this" is not a decision about the statement's fate.
func TestGatePhaseRequestsAProducer(t *testing.T) {
	srv, _ := capturingOPA(t, `{"result":{"allow":true,"request":{"ai_analysis":true}}}`)
	c := &policy.OPAClient{URL: srv.URL, Phase: policy.PhaseGate}

	var ec policy.EvalContext
	if v := c.EvaluateWith(stmt("SELECT * FROM customers", inspect.OpSelect, "customers"), &ec); v.Denied {
		t.Fatalf("the gate denied an allowed statement: %+v", v)
	}
	want, stated := ec.WantsRun(aiSource)
	if !stated || !want {
		t.Fatalf("WantsRun = (%v, %v), want the gate's request to reach the producer", want, stated)
	}
}

// A gate that vetoes must be distinguishable from a gate with no opinion,
// which is why Requested is three-valued rather than a set.
func TestGateVetoIsNotSilence(t *testing.T) {
	srv, _ := capturingOPA(t, `{"result":{"allow":true,"request":{"ai_analysis":false}}}`)
	c := &policy.OPAClient{URL: srv.URL, Phase: policy.PhaseGate}

	var ec policy.EvalContext
	c.EvaluateWith(stmt("SELECT 1", inspect.OpSelect), &ec)

	want, stated := ec.WantsRun(aiSource)
	if !stated {
		t.Fatal("an explicit request of false was read as no opinion")
	}
	if want {
		t.Error("a veto was read as a request")
	}
}

// A policy that both refuses the statement and asks for a producer must not
// be half-honored: the request is recorded even on a denial, so an operator
// reading the audit trail sees what the gate actually asked for.
func TestGateRequestSurvivesAGateDenial(t *testing.T) {
	srv, _ := capturingOPA(t, `{"result":{"allow":false,"message":"nope","request":{"ai_analysis":true}}}`)
	c := &policy.OPAClient{URL: srv.URL, Phase: policy.PhaseGate}

	var ec policy.EvalContext
	if v := c.EvaluateWith(stmt("DELETE FROM t", inspect.OpDelete, "t"), &ec); !v.Denied {
		t.Fatalf("the gate allowed a statement it refused: %+v", v)
	}
	if want, stated := ec.WantsRun(aiSource); !stated || !want {
		t.Errorf("WantsRun = (%v, %v) after a denying gate, want the request recorded", want, stated)
	}
}

// Turning the gate on must not require writing a gate rule. A gate is an
// optimization over a policy someone already wrote, so an undefined gate
// decision means "no opinion" rather than "deny everything".
func TestUndefinedGateDecisionAllows(t *testing.T) {
	srv, _ := capturingOPA(t, `{}`)
	c := &policy.OPAClient{URL: srv.URL, Phase: policy.PhaseGate, FailOpen: false}

	var ec policy.EvalContext
	if v := c.EvaluateWith(stmt("SELECT 1", inspect.OpSelect), &ec); v.Denied {
		t.Fatalf("an undefined gate decision denied: %+v", v)
	}
	if want, stated := ec.WantsRun(aiSource); stated {
		t.Errorf("an undefined gate expressed an opinion on a producer: %v", want)
	}
}

// The decide phase keeps the fail-closed reading of an undefined decision.
// It is the call that decides the statement's fate, so silence is not consent.
func TestUndefinedDecideDecisionStillDenies(t *testing.T) {
	srv, _ := capturingOPA(t, `{}`)
	c := &policy.OPAClient{URL: srv.URL, Phase: policy.PhaseDecide, FailOpen: false}

	if v := c.EvaluateWith(stmt("DELETE FROM t", inspect.OpDelete, "t"), producedContext()); !v.Denied {
		t.Fatalf("an undefined decide decision allowed: %+v", v)
	}
}

// --- chain ---------------------------------------------------------------

// contextEvaluator stands in for a producer: it writes what it was given onto
// the context and snapshots what the evaluators before it had established.
type contextEvaluator struct {
	notes   map[string]string
	reports *policy.Finding
	seen    *policy.EvalContext
}

func (c *contextEvaluator) Evaluate(inspect.Statement) policy.Verdict {
	return policy.Verdict{Annotations: c.notes}
}

func (c *contextEvaluator) EvaluateWith(_ inspect.Statement, ec *policy.EvalContext) policy.Verdict {
	// Cloned, because a later evaluator writing to the same maps would
	// otherwise rewrite what this one is meant to have seen.
	c.seen = &policy.EvalContext{
		Annotations: maps.Clone(ec.Annotations),
		Findings:    maps.Clone(ec.Findings),
		Requested:   maps.Clone(ec.Requested),
	}
	if c.reports != nil {
		ec.AddFinding(*c.reports)
	}
	return policy.Verdict{Annotations: c.notes}
}

// The forward edge: an evaluator sees what the ones before it established.
// Without it a producer's finding could never reach an OPA decision.
func TestChainThreadsFindingsForward(t *testing.T) {
	first := &contextEvaluator{reports: &policy.Finding{
		Source: aiSource, Rule: "risky-writes", Status: policy.FindingOK,
		Values: map[string]any{"risk_level": "high"},
	}}
	second := &contextEvaluator{}

	policy.Chain{first, second}.Evaluate(stmt("DELETE FROM t", inspect.OpDelete, "t"))

	if second.seen == nil {
		t.Fatal("the second evaluator was called without a context")
	}
	f, ok := second.seen.Finding(aiSource)
	if !ok {
		t.Fatalf("the first producer's finding did not reach the second evaluator: %v", second.seen.Findings)
	}
	if f.Values["risk_level"] != "high" {
		t.Errorf("risk_level reaching the second evaluator = %v, want high", f.Values["risk_level"])
	}
	if first.seen != nil && len(first.seen.Findings) != 0 {
		t.Errorf("the first evaluator saw findings nobody had written: %v", first.seen.Findings)
	}
}

// Annotations thread forward on the same edge, and they are a separate
// channel from findings: the audit trail's flat vocabulary must not dictate
// what a producer can tell a policy.
func TestChainThreadsAnnotationsForward(t *testing.T) {
	first := &contextEvaluator{notes: map[string]string{
		policy.AnnotationRiskLevel: "high",
	}}
	second := &contextEvaluator{}

	policy.Chain{first, second}.Evaluate(stmt("DELETE FROM t", inspect.OpDelete, "t"))

	if second.seen == nil {
		t.Fatal("the second evaluator was called without a context")
	}
	if got := second.seen.Annotations[policy.AnnotationRiskLevel]; got != "high" {
		t.Errorf("risk_level reaching the second evaluator = %q, want high", got)
	}
	if first.seen != nil && len(first.seen.Annotations) != 0 {
		t.Errorf("the first evaluator saw annotations nobody had written: %v", first.seen.Annotations)
	}
}

// The risk pair is the one annotation vocabulary Chain merges itself, because
// store/ rolls risk_level up onto the session record. A later evaluator must
// see the highest level with the action that produced it: merging the two
// keys independently yields {high, allow} out of a high->warn rule and a
// low->allow one, describing a mapping no rule configured.
func TestLaterEvaluatorSeesTheHighestRiskPair(t *testing.T) {
	loud := &contextEvaluator{notes: map[string]string{
		policy.AnnotationRiskLevel: "high", policy.AnnotationRiskAction: "warn",
	}}
	quiet := &contextEvaluator{notes: map[string]string{
		policy.AnnotationRiskLevel: "low", policy.AnnotationRiskAction: "allow",
	}}
	last := &contextEvaluator{}

	policy.Chain{loud, quiet, last}.Evaluate(stmt("SELECT 1", inspect.OpSelect))

	if last.seen == nil {
		t.Fatal("the last evaluator was called without a context")
	}
	if got := last.seen.Annotations[policy.AnnotationRiskLevel]; got != "high" {
		t.Errorf("risk_level = %q, want high; a later low rating must not downgrade it", got)
	}
	if got := last.seen.Annotations[policy.AnnotationRiskAction]; got != "warn" {
		t.Errorf("risk_action = %q, want warn; the action belongs to the winning level", got)
	}
}

// An evaluator that does not implement ContextualEvaluator must keep working
// untouched: a caller's own evaluator is one, and so was Rules until it grew
// findings.
func TestChainStillCallsPlainEvaluators(t *testing.T) {
	chain := policy.Chain{&contextEvaluator{}, &stubEvaluator{verdict: policy.Deny("plain", "denied")}}

	v := chain.Evaluate(stmt("DROP TABLE t", inspect.OpDrop, "t"))
	if !v.Denied {
		t.Fatal("a plain Evaluator behind a contextual one stopped being consulted")
	}
	if v.Rule != "plain" {
		t.Errorf("Rule = %q, want plain", v.Rule)
	}
}

// The gate's request reaches a producer that runs later in the same chain,
// which is the whole path a two-phase lane depends on.
func TestChainThreadsTheGateRequestForward(t *testing.T) {
	srv, _ := capturingOPA(t, `{"result":{"allow":true,"request":{"ai_analysis":true}}}`)
	gate := &policy.OPAClient{URL: srv.URL, Phase: policy.PhaseGate}
	producer := &contextEvaluator{}

	policy.Chain{gate, producer}.Evaluate(stmt("SELECT 1", inspect.OpSelect))

	if producer.seen == nil {
		t.Fatal("the producer was called without a context")
	}
	if want, stated := producer.seen.WantsRun(aiSource); !stated || !want {
		t.Errorf("WantsRun at the producer = (%v, %v), want the gate's request", want, stated)
	}
}
