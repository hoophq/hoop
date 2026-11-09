package analyzer

import (
	"container/list"
	"sync"
	"time"

	"github.com/hoophq/hoop/sidecar/policy"
)

// handoffPins keeps each require_approval the hand-off agent selected that
// filed a hold, keyed by handoffKey: the level's hand-off configuration and
// the exact content the agent read.
//
// The verdict cache cannot do this. It may be off, evict the entry, or hold
// a new classification, and the agent then decides again. A return-mode
// client resends the statement after approval; an agent that now selects
// allow forwards it and leaves the approval unspent, and one that selects
// block refuses a statement a person approved. A pin answers require_approval
// for the same level and content with no model call, so the resend files
// again and the plane answers from the approved review.
//
// Per process: a restart, a reload or another replica asks the agent again.
type handoffPins struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	order   *list.List // front is the most recently pinned
	ttl     time.Duration

	// now is injected so tests can expire pins without sleeping.
	now func() time.Time
}

type handoffPin struct {
	key      string
	reason   string
	expireAt time.Time
}

// maxHandoffPins bounds the pins one evaluator keeps. The oldest goes first;
// its statement then asks the agent again.
const maxHandoffPins = 1024

// handoffPinTTL is how long a pin lives after its last filing: the 30-minute
// hold. The plane's pending_ttl_sec and approval_ttl_sec are not served to
// the sidecar, so a pending approval request can outlive its pin.
const handoffPinTTL = ReviewWait

func newHandoffPins() *handoffPins {
	return &handoffPins{
		entries: map[string]*list.Element{},
		order:   list.New(),
		ttl:     handoffPinTTL,
		now:     time.Now,
	}
}

// get returns the reason of the live pin for key.
func (p *handoffPins) get(key string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	el, ok := p.entries[key]
	if !ok {
		return "", false
	}
	pin := el.Value.(*handoffPin)
	if p.now().After(pin.expireAt) {
		p.order.Remove(el)
		delete(p.entries, key)
		return "", false
	}
	return pin.reason, true
}

// pin keeps the decision for key, or renews it: every filing restarts the
// life of the hold it filed.
func (p *handoffPins) pin(key, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	expireAt := p.now().Add(p.ttl)
	if el, ok := p.entries[key]; ok {
		pin := el.Value.(*handoffPin)
		pin.reason, pin.expireAt = reason, expireAt
		p.order.MoveToFront(el)
		return
	}
	p.entries[key] = p.order.PushFront(&handoffPin{key: key, reason: reason, expireAt: expireAt})
	for p.order.Len() > maxHandoffPins {
		oldest := p.order.Back()
		p.order.Remove(oldest)
		delete(p.entries, oldest.Value.(*handoffPin).key)
	}
}

// unpin drops the pin for key.
func (p *handoffPins) unpin(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if el, ok := p.entries[key]; ok {
		p.order.Remove(el)
		delete(p.entries, key)
	}
}

// pinRef names the hand-off decision a hold pins once it is filed. The zero
// value pins nothing: a direct hold, or a fail-safe.
type pinRef struct {
	key, reason string
}

// holdSettled reports a hold verdict after which no resend needs the pin:
// the statement ran on a spent approval, or the plane answered a final
// status. A pending, reserved or given-back approval keeps it, as does an
// error that left the status unknown.
func holdSettled(v policy.Verdict) bool {
	if !v.Denied {
		return v.Review == nil || !v.Review.Reserved
	}
	if v.Review == nil {
		return false
	}
	switch v.Review.Status {
	case reviewRejected, reviewRevoked, reviewExpired, reviewExecuted:
		return true
	}
	return false
}
