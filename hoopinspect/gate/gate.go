// Package gate wires inspection, policy, audit and masking into the single
// decision a proxy actually needs to make: may these bytes proceed, and in
// what form.
//
// # Why this exists as its own package
//
// The four capabilities are independently useful and deliberately decoupled —
// a codec knows nothing about identity, a policy knows nothing about storage.
// But every real caller needs the same ordering, and getting that ordering
// wrong is a security bug rather than a style choice:
//
//  1. Inspect the bytes.
//  2. Evaluate policy on each statement.
//  3. Audit the verdict — BEFORE the statement reaches the upstream.
//  4. On the way back, mask, then audit what was masked.
//
// Step 3 is the one people get backwards. Auditing after forwarding means a
// crash between the two loses exactly the record of the statement that
// crashed you. Auditing first costs a write on the hot path and is worth it.
//
// # What it does not do
//
// No sockets, no TLS, no routing. A Gate is a function over bytes that
// returns a Decision; the caller owns the connection and enforces the answer.
// That boundary is what keeps this embeddable in libhoop's ReverseProxy, an
// Envoy ext_proc server, and a standalone sidecar without three variants.
package gate

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/audit"
	"github.com/hoophq/hoopinspect/policy"
	"github.com/hoophq/hoopinspect/session"
)

// Masker rewrites sensitive values out of a payload.
//
// Declared here as a narrow interface rather than imported from mask/ so a
// caller can supply their own engine — a shop with an existing DLP service
// plugs it in without forking the gate.
type Masker interface {
	// Mask returns the rewritten payload, the entity names that were
	// rewritten, and how many values changed. It must never return the
	// masked VALUES: an audit record of what you masked, in the clear, has
	// un-masked it.
	Mask(data []byte) (out []byte, entities []string, count int)
}

// Config assembles a Gate.
type Config struct {
	// Protocol selects the codec. Required.
	Protocol hoopinspect.Protocol

	// Policy evaluates statements. Optional: a nil Policy inspects and
	// audits without ever denying, which is the observe-only mode a team
	// runs for a week before turning enforcement on.
	Policy policy.Evaluator

	// Audit persists events. Optional but strongly recommended; nil means
	// nothing is recorded.
	Audit audit.Sink

	// Masker rewrites response payloads. Optional; nil disables masking.
	Masker Masker

	// FailOnAuditError makes a failed audit write deny the statement.
	//
	// Default false, and the default is the uncomfortable one: a broken
	// audit sink lets statements through unrecorded. Set this true where the
	// audit trail is a compliance requirement rather than an operational
	// convenience — then a sink outage stops traffic, which is the correct
	// behavior for a system whose whole purpose is "we can prove who did
	// what".
	FailOnAuditError bool

	// MaxBuffer bounds per-connection reassembly. Zero uses the inspector
	// default.
	MaxBuffer int
}

// Decision is the answer for one chunk of bytes.
type Decision struct {
	// Allowed reports whether the bytes may proceed. False means the caller
	// MUST NOT forward them.
	Allowed bool

	// Message is the operator-authored denial reason, meant to be surfaced
	// to the end user in the protocol's own error frame (a Postgres
	// ErrorResponse, an HTTP 403 body). A denial the user cannot read is a
	// support ticket.
	Message string

	// Rule identifies the policy rule that denied.
	Rule string

	// Statements are the statements decoded from this chunk, in order.
	// Present whether allowed or denied, so a caller can log them.
	Statements []hoopinspect.Statement

	// Payload is the bytes to forward. It differs from the input only when
	// masking rewrote something; otherwise it aliases the input.
	Payload []byte

	// Masked names the entity classes rewritten in Payload, and MaskedCount
	// how many values changed. Empty when nothing was masked.
	Masked      []string
	MaskedCount int

	// Err records an infrastructure failure (audit write, policy engine).
	// Allowed already reflects the configured fail-open/closed choice; this
	// is for logging.
	Err error
}

// Gate inspects one connection.
//
// It is stateful — the underlying codec reassembles messages across reads —
// and therefore NOT safe for concurrent use. One Gate per connection. The
// Policy, Audit and Masker it holds ARE shared and must themselves be
// concurrency-safe.
type Gate struct {
	cfg     Config
	sess    *session.Session
	client  *hoopinspect.Inspector
	server  *hoopinspect.Inspector
	policy  policy.Evaluator
	audit   audit.Sink
	masker  Masker
	polCtx  map[string]string
	started bool

	// mu guards the counters, which Close reads while a data-path goroutine
	// may still be incrementing them. The inspectors are not guarded: they
	// are per-direction and each direction has one reader.
	mu         sync.Mutex
	statements int
	denied     int
	closed     bool
}

// New builds a Gate for a session.
//
// Two inspectors are created, one per direction: a codec reassembles messages
// across reads, and interleaving both halves of a duplex stream into one
// reassembly buffer would corrupt both.
func New(sess *session.Session, cfg Config) (*Gate, error) {
	if sess == nil {
		return nil, errors.New("hoopinspect/gate: nil session")
	}
	if cfg.Protocol == "" {
		cfg.Protocol = sess.Protocol
	}
	if cfg.Protocol == "" {
		return nil, errors.New("hoopinspect/gate: no protocol configured")
	}

	client, err := hoopinspect.New(cfg.Protocol)
	if err != nil {
		return nil, fmt.Errorf("hoopinspect/gate: %w", err)
	}
	server, err := hoopinspect.New(cfg.Protocol)
	if err != nil {
		return nil, fmt.Errorf("hoopinspect/gate: %w", err)
	}
	if cfg.MaxBuffer > 0 {
		client.SetMaxBuffer(cfg.MaxBuffer)
		server.SetMaxBuffer(cfg.MaxBuffer)
	}

	sess.Protocol = cfg.Protocol
	return &Gate{
		cfg:    cfg,
		sess:   sess,
		client: client,
		server: server,
		policy: cfg.Policy,
		audit:  cfg.Audit,
		masker: cfg.Masker,
		polCtx: sess.PolicyContext(),
	}, nil
}

// Session returns the session this gate is inspecting.
func (g *Gate) Session() *session.Session { return g.sess }

// Start records the session-start event. Calling it is optional but makes an
// abandoned connection visible in the audit trail; without it a session that
// never issues a statement leaves no record at all.
//
// Idempotent.
func (g *Gate) Start(ctx context.Context) error {
	g.mu.Lock()
	if g.started {
		g.mu.Unlock()
		return nil
	}
	g.started = true
	g.mu.Unlock()

	if g.audit == nil {
		return nil
	}
	return g.audit.Write(ctx, audit.SessionStartEvent(g.sess))
}

// Request inspects bytes travelling client -> upstream and decides whether
// they may proceed.
//
// The returned Decision.Payload is what the caller should forward. On a
// denial the caller MUST NOT forward anything and should surface
// Decision.Message in the protocol's error frame.
//
// Bytes that do not complete a statement are buffered and produce an allowed
// Decision with no statements — a partial message cannot be judged, and
// holding the connection until it completes is the only correct answer.
func (g *Gate) Request(ctx context.Context, data []byte) Decision {
	return g.inspect(ctx, hoopinspect.FromClient, data)
}

// Response inspects bytes travelling upstream -> client.
//
// Masking applies here: Decision.Payload may differ from the input. A policy
// denial on a response is meaningful too — a rule can forbid a 5xx body or a
// result set touching a protected column — and the caller must honor it
// rather than forwarding what it already has in hand.
func (g *Gate) Response(ctx context.Context, data []byte) Decision {
	return g.inspect(ctx, hoopinspect.FromServer, data)
}

func (g *Gate) inspect(ctx context.Context, dir hoopinspect.Direction, data []byte) Decision {
	d := Decision{Allowed: true, Payload: data}

	insp := g.client
	if dir == hoopinspect.FromServer {
		insp = g.server
	}

	stmts, err := insp.Inspect(dir, data)
	d.Statements = stmts
	if err != nil {
		// A malformed stream is not a policy question. Report it and let the
		// caller decide whether to tear the connection down; forwarding
		// bytes we could not parse is the honest default, because the
		// upstream's own parser is the authority on its protocol.
		d.Err = fmt.Errorf("inspect: %w", err)
		g.writeAudit(ctx, audit.ErrorEvent(g.sess, d.Err))
	}

	for _, stmt := range stmts {
		verdict := g.evaluate(stmt)

		g.mu.Lock()
		g.statements++
		if verdict.Denied {
			g.denied++
		}
		g.mu.Unlock()

		// Audit BEFORE the caller forwards. A crash between the write and
		// the forward must not lose the record of the statement that ran.
		auditErr := g.writeAudit(ctx, audit.StatementEvent(
			g.sess, stmt, !verdict.Denied, verdict.Rule, verdict.Message))

		if auditErr != nil && g.cfg.FailOnAuditError {
			return Decision{
				Allowed:    false,
				Message:    "audit trail unavailable; statement refused",
				Rule:       "audit",
				Statements: stmts,
				Err:        auditErr,
			}
		}
		if auditErr != nil {
			d.Err = errors.Join(d.Err, auditErr)
		}

		if verdict.Denied {
			d.Allowed = false
			d.Message = verdict.Message
			d.Rule = verdict.Rule
			d.Payload = nil // nothing may be forwarded
			if verdict.Err != nil {
				d.Err = errors.Join(d.Err, verdict.Err)
			}
			return d
		}
		if verdict.Err != nil {
			d.Err = errors.Join(d.Err, verdict.Err)
		}
	}

	// Masking is response-side only: rewriting a client's request would
	// change the statement the upstream executes, which is a correctness
	// change, not a privacy control.
	//
	// It is also protocol-gated. Byte substitution changes the payload
	// LENGTH, and a length-prefixed binary protocol carries that length in a
	// frame header the masker knows nothing about. Masking a Postgres
	// DataRow in place desynchronizes the client instantly:
	//
	//   ada@example.com  (15 bytes) -> [REDACTED:email] (16 bytes)
	//
	// and psql reports "lost synchronization with server". Doing this
	// correctly requires re-framing inside the codec, which is real work
	// (see maskSafe). Until then, refusing to mask is the only honest
	// option: half-masked binary output is both corrupt AND still leaking.
	if dir == hoopinspect.FromServer && g.masker != nil && len(data) > 0 && maskSafe(g.cfg.Protocol) {
		out, entities, count := g.masker.Mask(data)
		if count > 0 {
			d.Payload = out
			d.Masked = entities
			d.MaskedCount = count
			g.writeAudit(ctx, audit.MaskedEvent(g.sess, entities, count))
		}
	}

	return d
}

// maskSafe reports whether in-place byte substitution can be applied to a
// protocol's response payload without corrupting its framing.
//
// Only HTTP qualifies today: its body is delimited by a Content-Length the
// codec can see, or by chunked framing, and the transport tolerates a body
// whose length changed as long as the header is consistent — which for the
// raw-stream path means masking is safe only because the relay forwards the
// whole response as one unit.
//
// The wire-database protocols all length-prefix their rows in binary frames.
// Supporting them means teaching each codec to re-frame after substitution,
// which is the natural next step and is deliberately not faked here.
func maskSafe(p hoopinspect.Protocol) bool {
	return p == hoopinspect.HTTP
}

// evaluate runs the policy, defaulting to allow when none is configured.
func (g *Gate) evaluate(stmt hoopinspect.Statement) policy.Verdict {
	if g.policy == nil {
		return policy.Allow()
	}
	// Attach the session facts so a Rego policy can reference the actor.
	// Done per statement rather than once because an OPAClient is shared
	// across connections and must not carry one session's context into
	// another's decision.
	if c, ok := g.policy.(*policy.OPAClient); ok {
		scoped := *c
		scoped.Context = g.polCtx
		return scoped.Evaluate(stmt)
	}
	return g.policy.Evaluate(stmt)
}

func (g *Gate) writeAudit(ctx context.Context, ev audit.Event) error {
	if g.audit == nil {
		return nil
	}
	return g.audit.Write(ctx, ev)
}

// Close ends the session and records the closing event with totals.
// Idempotent.
func (g *Gate) Close(ctx context.Context) error {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil
	}
	g.closed = true
	statements, denied := g.statements, g.denied
	g.mu.Unlock()

	g.sess.End()
	if g.audit == nil {
		return nil
	}
	return g.audit.Write(ctx, audit.SessionEndEvent(g.sess, statements, denied))
}

// Stats reports the running totals.
func (g *Gate) Stats() (statements, denied int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.statements, g.denied
}
