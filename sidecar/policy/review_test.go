package policy_test

import (
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// steps records the order evaluators and reviews ran in.
type steps []string

// requester stands in for the analyzer: it asks for a review and allows.
// accepted reports what RequestReview answered.
type requester struct {
	log      *steps
	verdict  policy.Verdict
	accepted bool
}

func (r *requester) Evaluate(inspect.Statement) policy.Verdict { return policy.Verdict{} }

func (r *requester) EvaluateWith(_ inspect.Statement, ec *policy.EvalContext) policy.Verdict {
	*r.log = append(*r.log, "analyzer")
	r.accepted = ec.RequestReview(policy.ReviewRequest{
		Mode: "hold", ModeSource: "listener",
		Resolve: func() policy.Verdict {
			*r.log = append(*r.log, "review")
			return r.verdict
		},
	})
	return policy.Verdict{}
}

// logged is an evaluator that records it ran and returns a fixed verdict.
type logged struct {
	log     *steps
	name    string
	verdict policy.Verdict
}

func (l *logged) Evaluate(inspect.Statement) policy.Verdict {
	*l.log = append(*l.log, l.name)
	return l.verdict
}

func sameSteps(t *testing.T, got steps, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ran %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ran %v, want %v", got, want)
		}
	}
}

// The bug ADR-0024 fixes: the hold ran inside the analyzer, so a decision
// placed after it denied a statement whose approval was already spent.
func TestTheChainRunsAReviewAfterEveryEvaluator(t *testing.T) {
	var log steps
	req := &requester{log: &log}
	decide := &logged{log: &log, name: "decide"}

	v := policy.Chain{req, decide}.Evaluate(stmt("DELETE FROM t", inspect.OpDelete, "t"))

	if v.Denied {
		t.Fatalf("an allowed review denied: %+v", v)
	}
	if !req.accepted {
		t.Fatal("a chain refused the review request")
	}
	sameSteps(t, log, "analyzer", "decide", "review")
}

func TestADenialAfterTheRequestRunsNoReview(t *testing.T) {
	var log steps
	req := &requester{log: &log}
	decide := &logged{log: &log, name: "decide", verdict: policy.Deny("opa", "no")}

	v := policy.Chain{req, decide}.Evaluate(stmt("DELETE FROM t", inspect.OpDelete, "t"))

	if !v.Denied || v.Rule != "opa" {
		t.Fatalf("the decide denial did not stand: %+v", v)
	}
	sameSteps(t, log, "analyzer", "decide")
}

// The review's verdict is the statement's, and its annotations reach the
// record beside the ones the chain already merged.
func TestTheReviewVerdictIsTheChainVerdict(t *testing.T) {
	var log steps
	req := &requester{log: &log, verdict: policy.Verdict{
		Denied: true, Rule: "payments", Message: "rejected",
		Annotations: map[string]string{"review_id": "9f97"},
	}}
	note := &logged{log: &log, name: "rules", verdict: policy.Verdict{
		Annotations: map[string]string{"guardrails.note": "x"},
	}}

	v := policy.Chain{note, req}.Evaluate(stmt("DELETE FROM t", inspect.OpDelete, "t"))

	if !v.Denied || v.Message != "rejected" {
		t.Fatalf("the review denial was lost: %+v", v)
	}
	if v.Annotations["review_id"] != "9f97" || v.Annotations["guardrails.note"] != "x" {
		t.Fatalf("annotations = %v, want both the review's and the rule's", v.Annotations)
	}
}

// A nested chain must not run the review at its own end: an evaluator after
// it in the outer chain could still deny.
func TestOnlyTheOutermostChainRunsTheReview(t *testing.T) {
	var log steps
	inner := policy.Chain{&requester{log: &log}}
	decide := &logged{log: &log, name: "decide"}

	policy.Chain{inner, decide}.Evaluate(stmt("DELETE FROM t", inspect.OpDelete, "t"))

	sameSteps(t, log, "analyzer", "decide", "review")
}

// Observe wraps the lane chain, and the chain inside still runs the review.
func TestAnObservedChainStillRunsTheReview(t *testing.T) {
	var log steps
	req := &requester{log: &log, verdict: policy.Deny("payments", "no reviewer")}

	v := policy.Observe{Evaluator: policy.Chain{req}}.EvaluateWith(
		stmt("DELETE FROM t", inspect.OpDelete, "t"), &policy.EvalContext{})

	if v.Denied || v.Annotations[policy.AnnotationWouldDeny] != "payments" {
		t.Fatalf("observe did not record the review denial: %+v", v)
	}
	sameSteps(t, log, "analyzer", "review")
}

// With no chain to run it, the producer must hold at once, or the review
// would never run and the statement would pass unreviewed.
func TestARequestOutsideAChainIsRefused(t *testing.T) {
	r := policy.ReviewRequest{Resolve: func() policy.Verdict { return policy.Verdict{} }}
	if (&policy.EvalContext{}).RequestReview(r) {
		t.Fatal("a context no chain owns accepted a review request")
	}
	var nilCtx *policy.EvalContext
	if nilCtx.RequestReview(r) {
		t.Fatal("a nil context accepted a review request")
	}
}

// Two producers asking for one statement deny with nothing filed: holding the
// second in place would spend its approval before a later decision could deny.
func TestASecondReviewRequestDeniesWithNothingFiled(t *testing.T) {
	var log steps
	first, second := &requester{log: &log}, &requester{log: &log}
	decide := &logged{log: &log, name: "decide"}

	v := policy.Chain{first, second, decide}.Evaluate(stmt("DELETE FROM t", inspect.OpDelete, "t"))

	if !first.accepted || !second.accepted {
		t.Fatalf("accepted = %v, %v; a refused ask would hold in place", first.accepted, second.accepted)
	}
	if !v.Denied || v.Source != policy.SourceReview {
		t.Fatalf("two review requests did not deny: %+v", v)
	}
	sameSteps(t, log, "analyzer", "analyzer", "decide")
}

// input.review rides the decide phase only. A gate runs before the producer
// asks, and a single-call lane must keep a byte-identical document.
func TestInputReviewRidesTheDecidePhaseOnly(t *testing.T) {
	for _, tc := range []struct {
		phase policy.Phase
		want  bool
	}{
		{policy.PhaseDecide, true},
		{policy.PhaseGate, false},
		{"", false},
	} {
		t.Run(string(tc.phase), func(t *testing.T) {
			srv, input := capturingOPA(t, `{"result": {"allow": true}}`)
			var log steps
			opa := &policy.OPAClient{URL: srv.URL, Phase: tc.phase}

			policy.Chain{&requester{log: &log}, opa}.Evaluate(
				stmt("DELETE FROM t", inspect.OpDelete, "t"))

			review, ok := input()["review"].(map[string]any)
			if ok != tc.want {
				t.Fatalf("input.review present = %v, want %v: %v", ok, tc.want, input())
			}
			if !tc.want {
				return
			}
			if review["required"] != true || review["mode"] != "hold" ||
				review["mode_source"] != "listener" {
				t.Fatalf("input.review = %v", review)
			}
		})
	}
}

func TestInputReviewIsAbsentWithNoRequest(t *testing.T) {
	srv, input := capturingOPA(t, `{"result": {"allow": true}}`)
	opa := &policy.OPAClient{URL: srv.URL, Phase: policy.PhaseDecide}

	policy.Chain{opa}.Evaluate(stmt("DELETE FROM t", inspect.OpDelete, "t"))

	if _, ok := input()["review"]; ok {
		t.Fatalf("input.review sent with no review pending: %v", input())
	}
}
