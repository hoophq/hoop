package policy_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// countingOPA allows everything and counts the calls, which is the whole
// cost these switches exist to remove.
func countingOPA(t *testing.T, result string) (string, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(result))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &calls
}

func response(text string) inspect.Statement {
	s := stmt(text, inspect.OpSelect)
	s.Direction = inspect.FromServer
	return s
}

// The operator's switch: a response statement costs no round trip, and the
// record says OPA never saw it rather than that OPA allowed it. A request
// statement is unaffected.
func TestSkipResponsesAnswersWithoutACall(t *testing.T) {
	url, calls := countingOPA(t, `{"result":{"denied":true,"message":"never consulted"}}`)
	c := &policy.OPAClient{URL: url, SkipResponses: true}

	v := c.Evaluate(response("row 1"))
	if v.Denied {
		t.Fatalf("a skipped response was denied: %+v", v)
	}
	if v.Annotations[policy.AnnotationOPASkipped] != "responses" {
		t.Errorf("annotations = %v; the trail cannot tell a skip from an allow", v.Annotations)
	}
	if calls.Load() != 0 {
		t.Errorf("OPA was called %d times for a response on a request-only client", calls.Load())
	}

	if v := c.Evaluate(stmt("SELECT 1", inspect.OpSelect)); !v.Denied {
		t.Error("the request side of a SkipResponses client was not evaluated")
	}
	if calls.Load() != 1 {
		t.Errorf("OPA was called %d times for one request", calls.Load())
	}
}

// A Requested veto on OPA's own source is honored like a producer honors
// one. This is the hop the gate uses to feed `responses: false` back.
func TestRequestedVetoSkipsTheCall(t *testing.T) {
	url, calls := countingOPA(t, `{"result":{"denied":true}}`)
	c := &policy.OPAClient{URL: url}

	ec := &policy.EvalContext{Requested: map[string]bool{policy.SourceOPA: false}}
	if v := c.EvaluateWith(response("row 1"), ec); v.Denied {
		t.Fatalf("a vetoed call denied: %+v", v)
	}
	if calls.Load() != 0 {
		t.Errorf("OPA was called %d times under a veto", calls.Load())
	}

	// An affirmative request is not a veto, and neither is silence.
	for _, ec := range []*policy.EvalContext{
		{Requested: map[string]bool{policy.SourceOPA: true}},
		{},
	} {
		if v := c.EvaluateWith(response("row 1"), ec); !v.Denied {
			t.Errorf("Requested=%v did not reach OPA", ec.Requested)
		}
	}
}

// The policy's own opt-out is recorded on the context of a request decision
// and only there: a response saying it answers a question nobody will ask.
func TestResponsesFalseIsRecordedOnRequestDecisions(t *testing.T) {
	url, _ := countingOPA(t, `{"result":{"allow":true,"responses":false}}`)
	c := &policy.OPAClient{URL: url}

	ec := &policy.EvalContext{}
	if v := c.EvaluateWith(stmt("SELECT 1", inspect.OpSelect), ec); v.Denied {
		t.Fatalf("allowed request denied: %+v", v)
	}
	if !ec.SkipResponses[policy.SourceOPA] {
		t.Errorf("SkipResponses = %v; the policy's opt-out was dropped", ec.SkipResponses)
	}

	ec = &policy.EvalContext{}
	c.EvaluateWith(response("row 1"), ec)
	if len(ec.SkipResponses) != 0 {
		t.Errorf("a response decision recorded an opt-out: %v", ec.SkipResponses)
	}

	// Absent and true both mean "keep asking".
	for _, result := range []string{`{"result":{"allow":true}}`, `{"result":{"allow":true,"responses":true}}`} {
		url, _ := countingOPA(t, result)
		ec := &policy.EvalContext{}
		(&policy.OPAClient{URL: url}).EvaluateWith(stmt("SELECT 1", inspect.OpSelect), ec)
		if len(ec.SkipResponses) != 0 {
			t.Errorf("%s recorded an opt-out: %v", result, ec.SkipResponses)
		}
	}
}
