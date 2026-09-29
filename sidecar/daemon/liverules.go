package daemon

import (
	"sync/atomic"

	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// ruleSwapper is a running lane a reload hands a rebuilt lane to: a relay
// lane's proxy.Server, or an endpoint lane's liveRules. Only the rule facts
// of the lane are read; everything else is in the reload baseline.
type ruleSwapper interface {
	swapLane(ln lane)
}

// relayRules adapts proxy.Server, whose exported SwapRules takes the pair.
type relayRules struct{ srv *proxy.Server }

func (r relayRules) swapLane(ln lane) { r.srv.SwapRules(ln.policy, ln.masker) }

// liveRules is an endpoint lane's rules, read once per RPC (grpc) or per
// connection (ssh) when the gate is built. It is the endpoint counterpart of
// proxy.Server's rule pointer: the libhoop server is built once, but its open
// callback reads the rules through this slot, so a reload reaches the next
// RPC or connection without a rebuild. An RPC or connection already open
// keeps the Gate it built and drains under its rules.
//
// Whether the lane masks at all is NOT here: it sets a libhoop option at
// build (mask_responses, mask_output), so turning masking on or off stays
// restart-bound. The reloader refuses that drift before it swaps anything.
type liveRules struct {
	cur atomic.Pointer[ruleSet]
}

// ruleSet travels as one unit, for the reason proxy.Server's pair does: a
// policy from one config generation must never run beside a masker from
// another.
type ruleSet struct {
	policy policy.Evaluator
	masker gate.Masker
	// holds reports that the lane's analyzer block holds for review. A grpc
	// lane then exposes analyzer.HeaderReviewMode to policy; it rides here
	// because the analyzer block swaps with the rules.
	holds bool
}

func newLiveRules(ln lane) *liveRules {
	r := &liveRules{}
	r.swapLane(ln)
	return r
}

// swapLane replaces the rules for every RPC or connection opened from now on.
func (r *liveRules) swapLane(ln lane) {
	r.cur.Store(&ruleSet{
		policy: ln.policy,
		masker: ln.masker,
		holds:  analyzerHolds(ln.cfg.Analyzer),
	})
}

func (r *liveRules) load() *ruleSet { return r.cur.Load() }
