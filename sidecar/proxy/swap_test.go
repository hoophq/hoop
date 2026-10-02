package proxy

import (
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

type stubEval struct{ name string }

func (s stubEval) Evaluate(inspect.Statement) policy.Verdict { return policy.Verdict{} }

// A connection captures the rules current at accept time, so the swap
// contract is entirely in the pointer: what Load returns after SwapLane is
// what the next accepted connection's Gate runs. The codec factory moves
// with the policy, so a policy that holds never runs beside a codec from a
// generation that dropped the body.
func TestSwapLaneReplacesWhatTheNextConnectionCaptures(t *testing.T) {
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
	built := false
	s.SwapLane(after, nil, func() inspect.Codec { built = true; return nil })

	got = s.rules.Load()
	if got.policy != policy.Evaluator(after) {
		t.Fatalf("SwapLane did not replace the policy")
	}
	if got.masker != nil {
		t.Fatalf("SwapLane kept a masker the new generation does not carry")
	}
	if got.codecFactory == nil {
		t.Fatal("SwapLane dropped the codec factory")
	}
	got.codecFactory()
	if !built {
		t.Error("the next connection builds its codec from the old factory")
	}
}
