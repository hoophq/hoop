package proxy

import (
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

type stubEval struct{ name string }

func (s stubEval) Evaluate(inspect.Statement) policy.Verdict { return policy.Verdict{} }

// A connection captures the rules current at accept time, so the swap
// contract is entirely in the pointer: what Load returns after SwapRules is
// what the next accepted connection's Gate runs.
func TestSwapRulesReplacesWhatTheNextConnectionCaptures(t *testing.T) {
	before := stubEval{name: "before"}
	s, err := NewServer(Config{
		Listen:   "127.0.0.1:0",
		Upstream: "h:5432",
		Protocol: inspect.Postgres,
		Policy:   before,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	got := s.rules.Load()
	if got.policy != policy.Evaluator(before) {
		t.Fatalf("the seeded policy is not what NewServer was given")
	}

	after := stubEval{name: "after"}
	s.SwapRules(after, nil)

	got = s.rules.Load()
	if got.policy != policy.Evaluator(after) {
		t.Fatalf("SwapRules did not replace the policy")
	}
	if got.masker != nil {
		t.Fatalf("SwapRules kept a masker the new generation does not carry")
	}
}
