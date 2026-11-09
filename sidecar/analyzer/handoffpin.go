package analyzer

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// handoffPins keeps the approval request each hand-off require_approval
// filed, keyed by handoffKey: the level's hand-off configuration and the
// exact content the agent read.
//
// The verdict cache cannot do this. It may be off, evict the entry, or hold
// a new classification, and the agent then decides again. A return-mode
// client resends the statement after approval; an agent that now selects
// allow forwards it and leaves the approval unspent, and one that selects
// block refuses a statement a person approved. A statement that hits a pin
// asks the backend about the pinned request instead (Reviewer.Claim): the
// plane, not this map, says whether the hold still stands.
//
// Per process: a restart, a reload or another replica asks the agent again.
type handoffPins struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	order   *list.List // front is the most recently filed
	ttl     time.Duration

	// now is injected so tests can expire pins without sleeping.
	now func() time.Time
}

type handoffPin struct {
	key      string
	reviewID string
	reason   string
	filedAt  time.Time
}

// maxHandoffPins bounds the pins one evaluator keeps. The oldest goes first;
// its statement then asks the agent again.
const maxHandoffPins = 1024

// handoffPinTTL is the most a pin lives after its filing: the 30-minute
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

// get returns the live pin for key.
func (p *handoffPins) get(key string) (handoffPin, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	el, ok := p.entries[key]
	if !ok {
		return handoffPin{}, false
	}
	pin := el.Value.(*handoffPin)
	if p.now().After(pin.filedAt.Add(p.ttl)) {
		p.order.Remove(el)
		delete(p.entries, key)
		return handoffPin{}, false
	}
	return *pin, true
}

// set pins the request a new filing named, replacing whatever key held.
func (p *handoffPins) set(key, reviewID, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pin := &handoffPin{key: key, reviewID: reviewID, reason: reason, filedAt: p.now()}
	if el, ok := p.entries[key]; ok {
		el.Value = pin
		p.order.MoveToFront(el)
		return
	}
	p.entries[key] = p.order.PushFront(pin)
	for p.order.Len() > maxHandoffPins {
		oldest := p.order.Back()
		p.order.Remove(oldest)
		delete(p.entries, oldest.Value.(*handoffPin).key)
	}
}

// drop removes the pin for key only while it still names reviewID: a newer
// filing for the same statement owns the pin it replaced it with.
func (p *handoffPins) drop(key, reviewID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if el, ok := p.entries[key]; ok && el.Value.(*handoffPin).reviewID == reviewID {
		p.order.Remove(el)
		delete(p.entries, key)
	}
}

// pinRef ties a hold to the hand-off decision that asked for it. The zero
// value pins nothing: a direct hold, or a fail-safe.
type pinRef struct {
	// key and reason are what a filing pins.
	key, reason string

	// reviewID is the pinned request this statement claims instead of
	// filing. again decides the hand-off afresh, with no pin, when that
	// request reached a final status; hold resolves a hold it selects.
	reviewID string
	again    func(hold func(pinRef) policy.Verdict) policy.Verdict
}

// holdPinned resolves a statement that hit a pin. It never files: it claims
// the pinned request. A release forwards; a pending request holds as a
// filed one does; a final status, or a request the backend no longer has,
// drops the pin and decides the hand-off again. A failed claim denies and
// keeps the pin.
func (e *Evaluator) holdPinned(ctx context.Context, stmt inspect.Statement, notes map[string]string,
	mode ReviewMode, source string, pin pinRef) policy.Verdict {
	id := pin.reviewID
	res, err := e.ask(ctx, func(c context.Context) (ReviewResult, error) {
		return e.cfg.Review.Claim(c, id)
	})
	switch {
	case errors.Is(err, ErrReviewNotFound):
		res = ReviewResult{ID: id, Status: reviewNotFound}
	case err != nil:
		e.errs.Add(1)
		notes[MetadataReviewID] = id
		v := e.denyHold(notes, id, "", "the approval request could not be checked")
		v.Err = err
		return v
	}
	if !res.Forward {
		switch res.Status {
		case reviewRejected, reviewRevoked, reviewExpired, reviewExecuted, reviewNotFound:
			e.pins.drop(pin.key, id)
			// The hold this statement asked for is over; a new decision
			// writes its own notes.
			delete(notes, MetadataReviewMode)
			delete(notes, MetadataReviewModeSource)
			return pin.again(func(p pinRef) policy.Verdict { return e.hold(ctx, stmt, notes, mode, source, p) })
		}
	}
	notes[MetadataReviewID] = id
	var v policy.Verdict
	switch {
	case res.Forward:
		v = e.release(ctx, notes, res)
	case res.Status == reviewPending && mode == ReviewReturn:
		return e.denyReturn(notes, id)
	case res.Status == reviewPending, res.Status == reviewProcessing && mode == ReviewHold:
		v = e.wait(ctx, id, notes)
	default:
		return e.denyHold(notes, id, res.Status, e.reviewReason(res))
	}
	if holdSettled(v) {
		e.pins.drop(pin.key, id)
	}
	return v
}

// reviewNotFound stands for a request the backend no longer has. The plane
// never sends it; holdPinned reads ErrReviewNotFound as it.
const reviewNotFound = "NOT_FOUND"

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
