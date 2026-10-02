package daemon

import (
	"sync"

	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// ruleSwapper is a running lane a reload hands a rebuilt lane to: a relay
// lane's proxy.Server, or an endpoint lane's liveRules. Only the rule facts
// of the lane are read; everything else is in the reload baseline.
//
// drained runs once, when no gate built from the outgoing rules is still
// open, so the reloader can stop reading that generation's analyzers only
// after the last statement that could reach them.
type ruleSwapper interface {
	swapLane(ln lane, drained func())
}

// relayRules adapts proxy.Server. The codec factory swaps with the rules;
// see newHTTPCodec.
//
// proxy.Server does not report when a connection that captured the old rules
// closes, so drained runs at once: a relay connection still open across a
// reload keeps evaluating through analyzers usage no longer reads.
type relayRules struct{ srv *proxy.Server }

func (r relayRules) swapLane(ln lane, drained func()) {
	r.srv.SwapLane(ln.policy, ln.masker, ln.codecFactory)
	drained()
}

// liveRules is an endpoint lane's rules, acquired once per RPC (grpc) or per
// connection (ssh) when the gate is built. It is the endpoint counterpart of
// proxy.Server's rule pointer: the libhoop server is built once, but its open
// callback reads the rules through this slot, so a reload reaches the next
// RPC or connection without a rebuild. An RPC or connection already open
// keeps the Gate it built and drains under its rules.
//
// Whether the lane masks at all is NOT here: it sets a libhoop option at
// build (mask_responses, mask_output), so turning masking on or off stays
// restart-bound. The reloader refuses that drift before it swaps anything.
//
// A mutex, not an atomic pointer: acquire must not hand out a generation
// that swapLane has already declared drained, and the count and the pointer
// have to move together for that to hold.
type liveRules struct {
	mu  sync.Mutex
	cur *ruleSet
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

	// Guarded by liveRules.mu. users counts gates built from this set and
	// not yet closed; drained is set when a swap retires the set, and runs
	// when users reaches zero after that.
	users   int
	retired bool
	drained func()
}

func newLiveRules(ln lane) *liveRules {
	return &liveRules{cur: newRuleSet(ln)}
}

func newRuleSet(ln lane) *ruleSet {
	return &ruleSet{
		policy: ln.policy,
		masker: ln.masker,
		holds:  analyzerHolds(ln.cfg.Analyzer),
	}
}

// swapLane replaces the rules for every RPC or connection opened from now on.
// drained runs once the last gate still using the outgoing rules closes, or
// now when none is open.
func (r *liveRules) swapLane(ln lane, drained func()) {
	r.mu.Lock()
	old := r.cur
	r.cur = newRuleSet(ln)
	old.retired = true
	idle := old.users == 0
	if !idle {
		old.drained = drained
	}
	r.mu.Unlock()
	if idle {
		drained()
	}
}

// acquire returns the current rules for one new gate. The caller MUST call
// the returned release exactly once when that gate is done; it is safe to
// call more than once.
func (r *liveRules) acquire() (*ruleSet, func()) {
	r.mu.Lock()
	s := r.cur
	s.users++
	r.mu.Unlock()
	var once sync.Once
	return s, func() { once.Do(func() { r.release(s) }) }
}

func (r *liveRules) release(s *ruleSet) {
	r.mu.Lock()
	s.users--
	var fire func()
	if s.retired && s.users == 0 {
		fire, s.drained = s.drained, nil
	}
	r.mu.Unlock()
	if fire != nil {
		fire()
	}
}
