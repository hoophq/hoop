// Package review gates a statement on a human approval held by the hoop
// gateway.
//
// # Deny and reconnect, never hold
//
// gate.Gate is a synchronous function over bytes and the relay dials the
// upstream on accept, so holding a statement while a person reads it would
// burn a connection slot against max_conns, trip idle_timeout_sec, get killed
// by driver-side statement timeouts anyway, and need a cancellation the
// policy.Evaluator interface cannot carry. Nothing here holds. A statement
// with no approval is REFUSED, the caller tears the session down as it does
// for any other denial, and the agent polls the gateway and reconnects.
//
// That is why this feature is tractable at all: the two blockers that used to
// sit under it — a notice channel so psql explains the wait, and cancellation
// — were artifacts of holding.
//
// # Two keys, and never one
//
// The design answers two questions and using one key for both is the mistake
// that turns a review gate into a review bypass:
//
//   - AUTHORIZATION ("may this execute?") is keyed on execKey, an exact
//     SHA-256 over the canonical statement text, plus scoping the gateway
//     applies from the credential it authenticated. The agent cannot
//     influence it.
//   - REQUEST identity ("is this a new ask, or a retry of one already
//     filed?") is keyed on the agent's own marker, and it only decides how
//     many reviews reach the queue.
//
// The authorization path never sees the marker. If it did, the agent would be
// choosing its own permissions — which is the prompt-injection case: a sandbox
// holding a legitimately approved review, putting that approval behind
// different SQL.
//
// # Placement in the chain
//
// The gate is two evaluators around the analyzer, the same shape the OPA
// client already uses for its gate and decide phases:
//
//	local rules → [opa gate] → review CLAIM → analyzer → [opa decide] → review DECIDE
//
// CLAIM runs first so an approved retry costs one gateway round-trip and no
// model call: a successful claim vetoes the analyzer through
// policy.EvalContext.Requested and the statement proceeds. DECIDE runs last so
// a statement a local rule, a Rego policy or a hard analyzer action already
// refused never files a review and never troubles a human.
//
// # Single use
//
// An approval authorizes exactly one execution. The claim consumes it in the
// same UPDATE that selects it, so a second attempt finds nothing and takes the
// denial path. Time-boxed grants are deliberately not implemented: a window
// and a use-count are different grants, and shipping both at once means the
// first deployment cannot tell which one let a statement through.
//
// # Approvals are never cached
//
// hoopinspect/store is an audit read-model, and a cached approval is a
// revocation that cannot be honored. Every retry consults the gateway. A short
// negative cache for a PENDING answer is fine and worth having, since a
// polling agent will hammer the path; APPROVED is never cached.
package review

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/analyzer"
	"github.com/hoophq/hoopinspect/policy"
)

// Annotation keys this gate writes onto a verdict, which the caller copies
// onto the audit event.
//
// A small fixed vocabulary, like every other producer's: audit redaction does
// not reach Event.Metadata, so anything written here lands in the trail
// verbatim. Only outcomes and server-minted ids go here, never a value the
// statement carried.
const (
	// AnnotationStatus is what the gate did: one of the Status* values.
	AnnotationStatus = "review_status"

	// AnnotationID is the gateway's review id, so a denied statement in the
	// trail can be joined to the review a human eventually answered.
	AnnotationID = "review_id"
)

// Outcomes recorded under AnnotationStatus.
const (
	// StatusApproved means an approved review was found and CONSUMED, and
	// the statement proceeded. It also explains why the trail shows the
	// analyzer as skipped on this statement: an approved retry is not
	// reclassified.
	StatusApproved = "approved"

	// StatusPending means a review is filed and waiting on a human. The
	// statement was refused.
	StatusPending = "pending"

	// StatusRefused means the gate would not even file a review, because it
	// cannot see everything that will execute or the lane requires a marker
	// the statement did not carry.
	StatusRefused = "refused"

	// StatusUnavailable means the gateway could not be reached or answered
	// an error. The statement was refused: a review gate that forwards when
	// its backend is down is not a review gate.
	StatusUnavailable = "unavailable"
)

// Rule is the name this gate denies under, in Verdict.Rule and in the trail.
const Rule = "require_review"

// Options assembles a Gate.
type Options struct {
	// Client talks to the gateway. Required.
	Client *Client

	// Triggers narrow which statements the CLAIM phase spends a round-trip
	// on. They are the triggers of the ai_analysis rules that actually map a
	// risk level to require_review, copied at startup.
	//
	// Without them the claim would fire on every statement on the lane,
	// which is a gateway call per query. With them it fires on the set the
	// operator already opted into paying a model for, which is the smallest
	// superset of "could be gated" available before classification.
	Triggers []analyzer.Trigger

	// RequireMarker refuses a gated statement that carries no marker,
	// instead of filing a review for it.
	//
	// Off by default. Turn it on for a busy lane: without a marker every
	// attempt is a new request, so a polling agent files one review per
	// attempt and the queue fills with duplicates of one question.
	RequireMarker bool

	// PendingTTL caches a PENDING answer for this long, so an agent
	// re-issuing in a tight loop does not turn one refusal into a stream of
	// gateway calls. Zero disables it.
	//
	// Negative answers only. Caching an APPROVED answer would be a
	// revocation that cannot be honored; caching a refusal only ever delays
	// an approval taking effect by at most this long, which is the safe
	// direction.
	PendingTTL time.Duration
}

// maxPendingEntries bounds the negative cache.
//
// A lane gates a handful of distinct statements at a time — every entry here
// is a statement waiting on a human — so the working set is tiny and the cap
// exists only so a pathological stream of distinct gated statements cannot
// grow it without limit.
const maxPendingEntries = 1024

// Gate holds everything the two phases share: the client, the trigger set and
// the negative cache. Safe for concurrent use; one Gate serves every
// connection on a lane.
type Gate struct {
	client        *Client
	triggers      []analyzer.Trigger
	requireMarker bool
	pendingTTL    time.Duration

	mu      sync.Mutex
	pending map[string]pendingEntry

	claims    atomic.Int64
	approvals atomic.Int64
	filed     atomic.Int64
	refusals  atomic.Int64
	errs      atomic.Int64
}

type pendingEntry struct {
	ticket  Ticket
	expires time.Time
}

// New builds a Gate.
func New(opts Options) (*Gate, error) {
	if opts.Client == nil {
		return nil, errors.New("hoopinspect/review: no client configured")
	}
	return &Gate{
		client:        opts.Client,
		triggers:      opts.Triggers,
		requireMarker: opts.RequireMarker,
		pendingTTL:    opts.PendingTTL,
		pending:       make(map[string]pendingEntry, 8),
	}, nil
}

// Phase selects which half of the gate an Evaluator performs.
type Phase string

const (
	// PhaseClaim consumes an existing approval, before the analyzer runs.
	PhaseClaim Phase = "claim"

	// PhaseDecide files a review and refuses, after every other evaluator
	// has had its say.
	PhaseDecide Phase = "decide"
)

// Evaluator is one phase of the gate as a policy.ContextualEvaluator.
type Evaluator struct {
	gate  *Gate
	phase Phase
}

// Claim returns the evaluator that belongs BEFORE the analyzer.
func (g *Gate) Claim() *Evaluator { return &Evaluator{gate: g, phase: PhaseClaim} }

// Decide returns the evaluator that belongs AFTER every other one.
func (g *Gate) Decide() *Evaluator { return &Evaluator{gate: g, phase: PhaseDecide} }

// Stats reports what the gate has done, for the /stats admin endpoint.
type Stats struct {
	// Claims counts authorization attempts against the gateway.
	Claims int64 `json:"claims"`

	// Approvals counts claims that found and consumed an approval, which is
	// the number of statements this gate let through.
	Approvals int64 `json:"approvals"`

	// Filed counts reviews created or re-reported to a human.
	Filed int64 `json:"filed"`

	// Refusals counts statements refused without filing anything: an
	// unobservable statement, or a missing marker on a lane that requires
	// one.
	Refusals int64 `json:"refusals"`

	// Errors counts gateway calls that failed.
	Errors int64 `json:"errors"`
}

// Stats returns a snapshot.
func (g *Gate) Stats() Stats {
	return Stats{
		Claims:    g.claims.Load(),
		Approvals: g.approvals.Load(),
		Filed:     g.filed.Load(),
		Refusals:  g.refusals.Load(),
		Errors:    g.errs.Load(),
	}
}

// Evaluate implements policy.Evaluator.
//
// The gate reads the connection and the session's correlation id off the
// evaluation context, so it cannot do anything useful without one. A caller
// that reaches here has composed the gate into a plain Evaluator slice rather
// than a Chain; refusing is the honest answer, because silently allowing would
// be a guardrail that does nothing.
func (e *Evaluator) Evaluate(stmt hoopinspect.Statement) policy.Verdict {
	return e.EvaluateWith(stmt, &policy.EvalContext{})
}

// EvaluateWith implements policy.ContextualEvaluator.
func (e *Evaluator) EvaluateWith(stmt hoopinspect.Statement, ec *policy.EvalContext) policy.Verdict {
	// Requests only. A response cannot be held for approval — whatever it
	// reports has already happened upstream — and the analyzer skips
	// responses for the same reason.
	if stmt.Direction != hoopinspect.FromClient {
		return policy.Verdict{}
	}
	if e.phase == PhaseClaim {
		return e.gate.claim(stmt, ec)
	}
	return e.gate.decide(stmt, ec)
}

// claim tries to consume an existing approval before anything is classified.
//
// It never denies. At this point nothing has established that the statement
// needs review at all: the analyzer has not run, and refusing on a gateway
// blip would break every triggered statement on the lane, including the ones
// that would have been rated low. A failure here degrades to "no approval
// found", and the decide phase — which does know the statement is gated —
// refuses there.
func (g *Gate) claim(stmt hoopinspect.Statement, ec *policy.EvalContext) policy.Verdict {
	if !g.candidate(stmt, ec) {
		return policy.Verdict{}
	}
	connection := connectionOf(ec)
	if connection == "" {
		// Nothing to scope a claim by. decide() turns this into a refusal
		// with a message naming the missing config; claiming blind would
		// ask the gateway a question with no answer.
		return policy.Verdict{}
	}
	id := identify(stmt, markerOf(ec))
	if !id.Observable || id.Canonical == "" {
		// No approval can exist for either: decide() refuses to file one
		// for a statement the gate cannot fully see, and there is nothing
		// to approve in a statement that is only a marker. Asking would be
		// a round trip with a known answer.
		return policy.Verdict{}
	}
	if _, held := g.cachedPending(connection, id.Hash); held {
		// A refusal filed moments ago. The answer cannot have become
		// APPROVED and been missed in any way that matters: the worst case
		// is that an approval landing inside the TTL takes effect up to
		// PendingTTL later.
		return policy.Verdict{}
	}

	g.claims.Add(1)
	ticket, err := g.client.Claim(context.Background(), connection, id.Hash)
	switch {
	case errors.Is(err, ErrNoApproval):
		return policy.Verdict{}
	case err != nil:
		g.errs.Add(1)
		// Carried, not acted on: policy.Chain accumulates Err without
		// stopping, so the trail records that the gate could not check
		// while the analyzer still gets to classify.
		return policy.Verdict{Err: err}
	}

	// Approved and consumed. The statement proceeds, and the analyzer is
	// vetoed: a human approved this exact text, so reclassifying it would
	// spend a model call to second-guess them and could refuse what they
	// allowed.
	//
	// EvalContext.Requested is the documented seam for exactly this — a
	// false entry vetoes a run the producer's own configuration would have
	// made — so no new channel is needed to say it.
	g.approvals.Add(1)
	g.forgetPending(connection, id.Hash)
	if ec.Requested == nil {
		ec.Requested = make(map[string]bool, 1)
	}
	ec.Requested[analyzer.Source] = false

	return policy.Verdict{Annotations: map[string]string{
		AnnotationStatus: StatusApproved,
		AnnotationID:     ticket.ReviewID,
	}}
}

// decide refuses a statement the analyzer flagged for review, filing the
// review that makes an approval possible.
//
// Every outcome other than "an approved, unconsumed review exists for this
// exact statement" ends the database session. PENDING, REJECTED, REVOKED, an
// already-consumed approval and an unreachable gateway all produce the same
// terminal denial; only the message differs. A surviving connection after a
// refused statement is a session in an indeterminate state, and it invites the
// pending semantics this design must not have.
func (g *Gate) decide(stmt hoopinspect.Statement, ec *policy.EvalContext) policy.Verdict {
	if !g.gated(ec) {
		return policy.Verdict{}
	}

	connection := connectionOf(ec)
	if connection == "" {
		// A lane whose listener sets no `connection` cannot be scoped, and
		// the config layer refuses that combination at startup. Reaching
		// here means the gate was assembled by hand; refuse rather than
		// forward.
		return g.refuse("this lane has no connection name, so a review cannot be scoped to it")
	}

	id := identify(stmt, markerOf(ec))
	if !id.Observable {
		return g.refuse(id.Why)
	}
	if id.Canonical == "" {
		// Nothing left after removing hoop's own marker. There is no
		// statement here to approve.
		return g.refuse("the statement is empty once hoop's own marker is removed")
	}
	if g.requireMarker && id.Marker == "" {
		return g.refuse("this lane requires a hoopdev:correlation_id marker on a statement that needs review; " +
			"prefix it with \"" + markerPrefix + "<id>\" on its own line")
	}

	if ticket, held := g.cachedPending(connection, id.Hash); held {
		return g.pendingVerdict(ticket, id.Hash)
	}

	req := Request{
		Connection:    connection,
		StatementHash: id.Hash,
		Statement:     id.Canonical,
		Marker:        id.Marker,
	}
	if f, ok := ec.Finding(analyzer.Source); ok {
		req.Rule = f.Rule
		if level, _ := f.Values[analyzer.FindingRiskLevel].(string); level != "" {
			req.RiskLevel = level
		}
	}

	g.filed.Add(1)
	ticket, err := g.client.Request(context.Background(), req)
	if err != nil {
		g.errs.Add(1)
		return policy.Verdict{
			Denied:  true,
			Rule:    Rule,
			Message: "this statement needs human approval and the review service is unreachable; refused",
			Err:     err,
			Annotations: map[string]string{
				AnnotationStatus: StatusUnavailable,
			},
		}
	}
	g.rememberPending(connection, id.Hash, *ticket)
	return g.pendingVerdict(*ticket, id.Hash)
}

// pendingVerdict renders the refusal a human has to act on.
//
// It carries two things the caller cannot get anywhere else. The URL, so a
// person driving psql through this lane reads where to go before their session
// drops. And the statement hash, so an AGENT can poll
// GET /relay/reviews?statement_hash= without reproducing this package's
// canonicalization — which it would otherwise have to guess at, down to
// whether the codec's splitter kept the trailing semicolon. The hash is a
// digest of a statement the caller itself wrote, so telling it back leaks
// nothing.
func (g *Gate) pendingVerdict(t Ticket, statementHash string) policy.Verdict {
	msg := "this statement needs human approval before it can run"
	if t.URL != "" {
		msg += "; review it at " + t.URL
	}
	msg += ". The session is closed: once it is approved, reconnect and re-issue the same statement" +
		" (poll statement_hash=" + statementHash + ")."
	return policy.Verdict{
		Denied:  true,
		Rule:    Rule,
		Message: msg,
		Annotations: map[string]string{
			AnnotationStatus: StatusPending,
			AnnotationID:     t.ReviewID,
		},
	}
}

// refuse denies without filing anything.
func (g *Gate) refuse(why string) policy.Verdict {
	g.refusals.Add(1)
	return policy.Verdict{
		Denied:  true,
		Rule:    Rule,
		Message: "this statement needs human approval and cannot be submitted for it: " + why,
		Annotations: map[string]string{
			AnnotationStatus: StatusRefused,
		},
	}
}

// candidate reports whether the CLAIM phase should spend a round-trip on this
// statement.
//
// It mirrors analyzer.Evaluator's own eligibility test, and deliberately: a
// statement the analyzer will not classify cannot be flagged for review, so
// claiming for it is a call with a known answer. A gate-phase policy that
// named the analyzer overrides the configured triggers in both directions,
// exactly as it does for the analyzer itself.
func (g *Gate) candidate(stmt hoopinspect.Statement, ec *policy.EvalContext) bool {
	if want, stated := ec.WantsRun(analyzer.Source); stated {
		return want
	}
	for _, t := range g.triggers {
		if t.Matches(stmt) {
			return true
		}
	}
	return false
}

// gated reports whether the analyzer resolved this statement to require_review.
//
// The action is read from the analyzer's FINDING rather than recomputed from a
// second copy of the action map. Two copies of "which risk level needs a
// human" is how one of them ends up stale, and the finding is the channel
// producers already use to hand a fact to whatever decides next.
//
// An unanswered finding never gates. A classification that did not happen — a
// provider outage, a spent budget, a vetoed run — is not a flag, and the
// analyzer has already applied its own fail-open/fail-closed choice to that
// case.
func (g *Gate) gated(ec *policy.EvalContext) bool {
	f, ok := ec.Finding(analyzer.Source)
	if !ok || !f.Answered() {
		return false
	}
	action, _ := f.Values[analyzer.FindingAction].(string)
	return action == string(analyzer.ActionRequireReview)
}

// connectionOf reads the lane's resource name off the evaluation context. It
// is the same value session.PolicyContext publishes to Rego, so the gate and a
// policy always agree about which resource a statement reached.
func connectionOf(ec *policy.EvalContext) string {
	if ec == nil {
		return ""
	}
	return strings.TrimSpace(ec.Context["connection"])
}

// markerOf reads the session-level correlation id, which is the fallback
// request identity for a statement that carries no marker of its own — and the
// only one available on a protocol with nowhere to put a comment.
func markerOf(ec *policy.EvalContext) string {
	if ec == nil {
		return ""
	}
	v := strings.TrimSpace(ec.Context["correlation_id"])
	if !validMarker(v) {
		return ""
	}
	return v
}

func pendingKey(connection, hash string) string { return connection + "\x00" + hash }

func (g *Gate) cachedPending(connection, hash string) (Ticket, bool) {
	if g.pendingTTL <= 0 {
		return Ticket{}, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	e, ok := g.pending[pendingKey(connection, hash)]
	if !ok || time.Now().After(e.expires) {
		return Ticket{}, false
	}
	return e.ticket, true
}

func (g *Gate) rememberPending(connection, hash string, t Ticket) {
	if g.pendingTTL <= 0 {
		return
	}
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.pending) >= maxPendingEntries {
		g.evictLocked(now)
	}
	g.pending[pendingKey(connection, hash)] = pendingEntry{
		ticket:  t,
		expires: now.Add(g.pendingTTL),
	}
}

func (g *Gate) forgetPending(connection, hash string) {
	if g.pendingTTL <= 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.pending, pendingKey(connection, hash))
}

// evictLocked drops expired entries, and if that frees nothing, clears the map.
//
// Clearing rather than evicting one victim: every entry is a short-lived
// negative answer, so losing the lot costs one extra gateway call each and
// keeps the bound absolute without a second data structure to hold an LRU
// order.
func (g *Gate) evictLocked(now time.Time) {
	for k, e := range g.pending {
		if now.After(e.expires) {
			delete(g.pending, k)
		}
	}
	if len(g.pending) >= maxPendingEntries {
		clear(g.pending)
	}
}

// Describe renders the gate for the startup log and the /config view.
func (g *Gate) Describe() string {
	return fmt.Sprintf("gateway=%s require_marker=%v pending_ttl=%s",
		g.client.Endpoint(), g.requireMarker, g.pendingTTL)
}
