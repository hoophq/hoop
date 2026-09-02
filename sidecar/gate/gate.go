// Package gate wires inspection, policy, audit and masking into the single
// decision a proxy has to make: may these bytes proceed, and in what form.
//
// # The ordering this package enforces
//
// The four capabilities are independently useful and decoupled: a codec
// knows nothing about identity, a policy knows nothing about storage. Every
// caller needs the same ordering, and getting that ordering wrong is a
// security bug:
//
//  1. Inspect the bytes.
//  2. Evaluate policy on each statement.
//  3. Audit the verdict, BEFORE the statement reaches the upstream.
//  4. On the way back, mask, then audit what was masked.
//
// Step 3 is the one to get right. Auditing after forwarding means a crash
// between the two loses the record of the statement that crashed you.
// Auditing first costs a write on the hot path and buys that record.
//
// # Scope
//
// A Gate is a function over bytes that returns a Decision. Sockets, TLS and
// routing stay with the caller, which owns the connection and enforces the
// answer. That boundary keeps a single implementation embeddable in libhoop's
// ReverseProxy, an Envoy ext_proc server, and a standalone sidecar.
package gate

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/session"
)

// Masker rewrites sensitive values out of a payload.
//
// Declared here as a narrow interface instead of imported from a masking
// package, so you can supply your own engine: a shop with an existing DLP
// service plugs it in without forking the gate.
type Masker interface {
	// Mask returns the rewritten payload, the entity names that were
	// rewritten, and how many values changed. It must never return the
	// masked VALUES: an audit record of what you masked, in the clear, has
	// un-masked it.
	Mask(data []byte) (out []byte, entities []string, count int)

	// MaskCell rewrites one already-delimited value, such as a database
	// result-set cell, and is told the column it came from.
	//
	// It exists because Mask scans for values inside an opaque blob. Once
	// the protocol has named the columns, a rule can say "mask the ssn
	// column": deterministic, and the only way to protect a column whose
	// contents no pattern detector recognizes.
	//
	// column is empty when the protocol did not name it.
	MaskCell(column string, value []byte) (out []byte, entities []string, count int)
}

// Reframer masks a length-prefixed response stream by rebuilding its frames.
//
// A codec implements it when byte substitution would corrupt the protocol:
// every Postgres DataRow declares its own size and the size of each column, so
// replacing a value without recomputing both desynchronizes the client. The
// codec decodes the frames, hands each cell to the masker, and re-encodes.
//
// Rewrite MAY return fewer bytes than it received: rows are held until their
// result set ends, because a row cannot be rebuilt once forwarded. Flush
// releases whatever is held and MUST be called when the connection closes, or
// the client's last rows are silently dropped.
type Reframer interface {
	Rewrite(data []byte, mask func(column string, value []byte) []byte) ([]byte, inspect.ReframeResult, error)
	Flush(mask func(column string, value []byte) []byte) []byte
}

// Duplex marks a codec whose two directions decode against ONE shared state.
//
// Most codecs are per-direction: a Postgres request decoder needs nothing the
// response decoder knows, so giving each direction its own instance keeps
// their reassembly buffers apart and costs nothing.
//
// MySQL is not like that, and the difference is not cosmetic. What a server
// packet MEANS depends on state that only ever appears on the client side:
// the capability flags latched from the handshake response decide whether a
// result set ends with EOF or OK, the command in flight decides whether a
// leading 0x00 opens an OK packet or a column count, and a COM_STMT_EXECUTE
// carries an id whose SQL text arrived in an earlier COM_STMT_PREPARE.
//
// Split across two instances, each one sees half a conversation: the server
// decoder reports every reply against command 0x00, never learns the
// negotiated capabilities, and cannot name the columns a masking rule
// matches — so masking silently does nothing. That failure is invisible from
// the outside, which is why this is an interface the gate honours rather
// than a convention a codec is trusted to document.
type Duplex interface {
	// Duplex reports that both directions must share one codec instance.
	Duplex()
}

// Config assembles a Gate.
type Config struct {
	// Protocol selects the codec. Required.
	Protocol inspect.Protocol

	// Policy evaluates statements. Optional: a nil Policy inspects and
	// audits without ever denying, the observe-only mode you run for a week
	// before turning enforcement on.
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
	// audit trail is a compliance requirement. A sink outage then stops
	// traffic, the correct behavior for a system that exists to prove who
	// did what.
	FailOnAuditError bool

	// MaxBuffer bounds per-connection reassembly. Zero uses the inspector
	// default.
	MaxBuffer int

	// CodecFactory overrides how this Gate builds its two codecs, one per
	// direction. Nil uses the registry, which is what every lane did before
	// this field existed.
	//
	// It exists because a codec's capture options are a per-lane decision
	// the registry cannot express: Register takes a factory with no
	// arguments, so codec/http registers New(Options{}) and every lane in
	// the process shares those defaults. An HTTP lane that must expose
	// request bodies to policy has to supply its own factory.
	//
	// It MUST return a fresh codec per call. Two connections sharing one
	// stateful codec corrupt each other's reassembly buffer, which is the
	// same reason Register takes a factory rather than an instance.
	CodecFactory func() inspect.Codec
}

// Decision is the answer for one chunk of bytes.
type Decision struct {
	// Allowed reports whether the bytes may proceed. False means the caller
	// MUST NOT forward them.
	Allowed bool

	// Message is the operator-authored denial reason, meant to be surfaced
	// to the end user in the protocol's own error frame (a Postgres
	// ErrorResponse, an HTTP 403 body). A denial the user cannot read turns
	// into a support ticket.
	Message string

	// Rule identifies the policy rule that denied.
	Rule string

	// Statements are the statements decoded from this chunk, in order.
	// Present whether allowed or denied, so a caller can log them.
	Statements []inspect.Statement

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

// errMaskSkippedStaleLength records a response body forwarded unmasked
// because its Content-Length could not be corrected. See maskBySubstitution.
var errMaskSkippedStaleLength = errors.New(
	"sidecar/gate: response body forwarded unmasked: its header block is " +
		"not in this buffer, so Content-Length cannot be corrected and a " +
		"masked body would be truncated by the client")

// Gate inspects one connection.
//
// It is stateful, because the underlying codec reassembles messages across
// reads, so it is NOT safe for concurrent use. Use one Gate per connection.
// The Policy, Audit and Masker it holds ARE shared and must themselves be
// concurrency-safe.
type Gate struct {
	cfg     Config
	sess    *session.Session
	client  *inspect.Inspector
	server  *inspect.Inspector
	policy  policy.Evaluator
	audit   audit.Sink
	masker  Masker
	polCtx  map[string]string
	started bool

	// reframer is the server-side codec when it can rebuild its own frames,
	// nil otherwise. Set from the server Inspector's codec at construction,
	// so the data path does not type-assert per packet.
	reframer Reframer

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
// New creates two inspectors, one per direction: a codec reassembles messages
// across reads, and interleaving both halves of a duplex stream into one
// reassembly buffer would corrupt both.
//
// A codec implementing Duplex is the exception. It gets ONE instance driving
// both inspectors, because its two directions are not independent — see
// Duplex. The reassembly buffers still do not mix: those live on the
// Inspector, one per direction, and only the codec's protocol state is
// shared.
func New(sess *session.Session, cfg Config) (*Gate, error) {
	if sess == nil {
		return nil, errors.New("sidecar/gate: nil session")
	}
	if cfg.Protocol == "" {
		cfg.Protocol = sess.Protocol
	}
	if cfg.Protocol == "" {
		return nil, errors.New("sidecar/gate: no protocol configured")
	}

	newCodec := func() (inspect.Codec, error) {
		if cfg.CodecFactory == nil {
			insp, err := inspect.New(cfg.Protocol)
			if err != nil {
				return nil, err
			}
			return insp.Codec(), nil
		}
		c := cfg.CodecFactory()
		if c == nil {
			return nil, errors.New("sidecar/gate: CodecFactory returned nil")
		}
		if got := c.Protocol(); got != cfg.Protocol {
			return nil, fmt.Errorf("sidecar/gate: CodecFactory returned a %q codec for a %q lane",
				got, cfg.Protocol)
		}
		return c, nil
	}

	clientCodec, err := newCodec()
	if err != nil {
		return nil, fmt.Errorf("sidecar/gate: %w", err)
	}
	serverCodec := clientCodec
	if _, duplex := clientCodec.(Duplex); !duplex {
		if serverCodec, err = newCodec(); err != nil {
			return nil, fmt.Errorf("sidecar/gate: %w", err)
		}
	}

	client := inspect.NewWithCodec(clientCodec)
	server := inspect.NewWithCodec(serverCodec)
	if cfg.MaxBuffer > 0 {
		client.SetMaxBuffer(cfg.MaxBuffer)
		server.SetMaxBuffer(cfg.MaxBuffer)
	}

	sess.Protocol = cfg.Protocol
	g := &Gate{
		cfg:    cfg,
		sess:   sess,
		client: client,
		server: server,
		policy: cfg.Policy,
		audit:  cfg.Audit,
		masker: cfg.Masker,
		polCtx: sess.PolicyContext(),
	}
	// Discover the optional re-framing capability once, so the data path
	// does not type-assert per packet. A codec that cannot rebuild its own
	// frames leaves this nil and masking falls back to substitution, which
	// MaskSupported gates.
	if rf, ok := server.Codec().(Reframer); ok {
		g.reframer = rf
	}
	return g, nil
}

// Session returns the session this gate is inspecting.
func (g *Gate) Session() *session.Session { return g.sess }

// Start records the session-start event. Calling it is optional but makes an
// abandoned connection visible in the audit trail; without it a session that
// never issues a statement leaves no record.
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
// Decision with no statements. A partial message cannot be judged, so the
// gate holds the connection until it completes.
func (g *Gate) Request(ctx context.Context, data []byte) Decision {
	return g.inspect(ctx, inspect.FromClient, data)
}

// Response inspects bytes travelling upstream -> client.
//
// Masking applies here: Decision.Payload may differ from the input. A policy
// denial on a response is meaningful too. A rule can forbid a 5xx body or a
// result set touching a protected column, and the caller must honor the
// denial instead of forwarding what it already has in hand.
func (g *Gate) Response(ctx context.Context, data []byte) Decision {
	return g.inspect(ctx, inspect.FromServer, data)
}

// FlushResponse returns any response bytes the codec is still holding.
//
// A re-framing codec buffers rows until their result set ends, because a row
// cannot be rebuilt once forwarded. If the connection closes mid-result-set
// those rows would be dropped, silently truncating the client's output, a
// worse failure than masking late. The relay MUST call this before it stops
// pumping, and forward whatever comes back.
//
// Returns nil when nothing is held or the codec does not re-frame.
func (g *Gate) FlushResponse() []byte {
	if g.reframer == nil {
		return nil
	}
	if g.masker == nil {
		return g.reframer.Flush(nil)
	}
	return g.reframer.Flush(func(column string, value []byte) []byte {
		out, _, n := g.masker.MaskCell(column, value)
		if n == 0 {
			return value
		}
		return out
	})
}

func (g *Gate) inspect(ctx context.Context, dir inspect.Direction, data []byte) Decision {
	d := Decision{Allowed: true, Payload: data}

	insp := g.client
	if dir == inspect.FromServer {
		insp = g.server
	}

	stmts, err := insp.Inspect(dir, data)
	d.Statements = stmts
	if err != nil {
		// A malformed stream falls outside policy. Report it and let the
		// caller decide whether to tear the connection down; forwarding
		// bytes the gate could not parse is the honest default, because the
		// upstream's own parser is the authority on its protocol.
		d.Err = fmt.Errorf("inspect: %w", err)
		g.writeAudit(ctx, audit.ErrorEvent(g.sess, d.Err))

		// ErrStreamUnsafe is the exception to that default, and it inverts
		// it. The codec parsed these bytes and is reporting that forwarding
		// them ends its ability to see anything further. Honest-default
		// forwarding would hand the client a redirect and lose the session,
		// so this denies REGARDLESS of policy: no rule configured it, and
		// none can switch it off.
		//
		// ErrBufferOverflow denies for the same reason. It means one
		// message exceeded the reassembly budget without ever completing,
		// so the codec never produced a statement for it — forwarding the
		// chunks would run that statement with policy having seen nothing.
		// MySQL is what makes it reachable: a single logical message is
		// legal up to 16 MiB there against a default 8 MiB budget, so a
		// destructive statement padded past the limit would otherwise pass
		// a lane configured to refuse it.
		if errors.Is(err, inspect.ErrStreamUnsafe) ||
			errors.Is(err, inspect.ErrBufferOverflow) {
			d.Allowed = false
			d.Rule = "stream-unsafe"
			d.Message = err.Error()
			g.mu.Lock()
			g.denied++
			g.mu.Unlock()
			return d
		}
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
		ev := audit.StatementEvent(
			g.sess, stmt, !verdict.Denied, verdict.Rule, verdict.Message)
		// An evaluator's annotations (the AI analyzer's risk level) ride
		// onto the event here rather than through StatementEvent, because
		// they belong to the VERDICT and not to the statement: the same
		// statement classified twice can carry different risk.
		if len(verdict.Annotations) > 0 {
			if ev.Metadata == nil {
				ev.Metadata = make(map[string]string, len(verdict.Annotations))
			}
			for k, v := range verdict.Annotations {
				ev.Metadata[k] = v
			}
		}
		auditErr := g.writeAudit(ctx, ev)

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
	// change the statement the upstream executes, which breaks correctness
	// instead of protecting privacy.
	//
	// Two mechanisms, because two kinds of framing:
	//
	//   - Byte substitution, for a payload whose length is declared in a
	//     header the gate can find and correct (HTTP's Content-Length).
	//   - Re-framing, for a length-prefixed binary protocol where every row
	//     and column carries its own size. Substituting bytes there
	//     desynchronizes the client; the codec rebuilds the frames instead.
	if dir == inspect.FromServer && g.masker != nil && len(data) > 0 {
		switch {
		case g.reframer != nil:
			g.maskByReframing(ctx, &d, data)
		case substitutionSafe(g.cfg.Protocol):
			g.maskBySubstitution(ctx, &d, data)
		}
	}

	return d
}

// maskBySubstitution rewrites the payload in place and corrects the declared
// length. HTTP only.
//
// Masking and retagging are one decision, not two. If the length cannot be
// corrected the ORIGINAL bytes go out unmasked, because a masked body behind
// a stale Content-Length is read to the old length and stops mid-token: the
// client sees a corrupt response rather than a protected one.
func (g *Gate) maskBySubstitution(ctx context.Context, d *Decision, data []byte) {
	out, entities, count := g.masker.Mask(data)
	if count == 0 {
		return
	}
	// Masking changes the body LENGTH, and for HTTP the length is also
	// declared in a header the masker never looked at. Leaving Content-Length
	// stale makes the client read exactly that many bytes and stop
	// mid-document. The truncated response looks like a corrupt upstream
	// rather than a masking bug.
	out, ok := retagContentLength(out, len(out)-len(data))
	if !ok {
		// Commonly a body chunk whose header block already went out, which
		// happens whenever the upstream's header and body land in separate
		// TCP reads. Nothing here can move the number the client was given.
		//
		// Audited, never silent: this is sensitive data going out in the
		// clear, and an operator comparing a masked response against an
		// unmasked one needs the reason in the same trail as everything else.
		g.writeAudit(ctx, audit.ErrorEvent(g.sess, errMaskSkippedStaleLength))
		return
	}
	d.Payload = out
	d.Masked = entities
	d.MaskedCount = count
	g.writeAudit(ctx, audit.MaskedEvent(g.sess, entities, count))
}

// maskByReframing hands the stream to the codec, which masks each cell and
// rebuilds the frames around the results.
//
// The codec may return FEWER bytes than it was given: rows are held back until
// their result set ends, because a row cannot be rebuilt once forwarded. That
// is safe for a relay, since the held bytes arrive on a later call or on
// Close, and it is why Gate.Close flushes.
func (g *Gate) maskByReframing(ctx context.Context, d *Decision, data []byte) {
	var (
		entities []string
		seen     = map[string]bool{}
	)
	out, res, err := g.reframer.Rewrite(data, func(column string, value []byte) []byte {
		masked, names, n := g.masker.MaskCell(column, value)
		if n == 0 {
			return value
		}
		for _, e := range names {
			if !seen[e] {
				seen[e] = true
				entities = append(entities, e)
			}
		}
		return masked
	})
	if err != nil {
		// A malformed response falls outside masking. Forward what the codec
		// produced and record it; the upstream's own client is the authority
		// on its protocol.
		d.Err = errors.Join(d.Err, err)
		g.writeAudit(ctx, audit.ErrorEvent(g.sess, err))
	}

	d.Payload = out
	if res.Cells > 0 {
		sort.Strings(entities)
		d.Masked = entities
		d.MaskedCount = res.Cells
		g.writeAudit(ctx, audit.MaskedEvent(g.sess, entities, res.Cells))
	}
}

// MaskSupported reports whether a protocol's response payload can be masked
// by either mechanism.
//
// Two ways a payload can be rewritten safely:
//
//   - Substitution, when the length is declared in a header the gate can
//     correct. HTTP's Content-Length; see retagContentLength.
//   - Re-framing, when the codec can rebuild its own frames around the new
//     values. Postgres, whose every row and column is length-prefixed; see
//     the Reframer interface.
//
// MaskSupported asks the codec instead of listing protocols, so adding a
// re-framing codec does not require also remembering to edit this.
//
// Exported so a configuration layer can REFUSE masking on a protocol that
// supports neither, instead of accepting the setting and silently never
// masking. One predicate, so the config check and the data path cannot drift.
func MaskSupported(p inspect.Protocol) bool {
	if p == inspect.HTTP {
		return true
	}
	insp, err := inspect.New(p)
	if err != nil {
		return false
	}
	_, ok := insp.Codec().(Reframer)
	return ok
}

// substitutionSafe reports whether the byte-substitution path applies. The
// data path asks this after finding no reframer.
func substitutionSafe(p inspect.Protocol) bool {
	return p == inspect.HTTP
}

// evaluate runs the policy, defaulting to allow when none is configured.
func (g *Gate) evaluate(stmt inspect.Statement) policy.Verdict {
	if g.policy == nil {
		return policy.Allow()
	}
	// Attach the session facts so a Rego policy can reference the actor.
	//
	// They ride on the evaluation context rather than on a copy of the
	// client. An OPAClient is shared across connections and must not carry
	// one session's context into another's decision, and a lane that
	// consults OPA on both sides of the analyzer holds TWO of them inside a
	// policy.Chain, which a type assertion for a bare client silently
	// misses, leaving input.context empty on exactly the lanes that need it.
	if ce, ok := g.policy.(policy.ContextualEvaluator); ok {
		return ce.EvaluateWith(stmt, &policy.EvalContext{Context: g.polCtx})
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
