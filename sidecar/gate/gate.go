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

// rewriteActivator lets a stateful codec retain request correlation before
// any server bytes arrive. Stateless reframers do not need it.
type rewriteActivator interface {
	EnableRewrite()
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

// StreamFilter transforms connection bytes before either inspection or
// forwarding. It is for negotiation fields that decide every later packet's
// layout: ClickHouse clamps both advertised revisions here so the peers and
// decoder all speak the same bounded vocabulary.
//
// A filter may hold an incomplete prefix and return no bytes. Any error is
// fatal: once a filter has retained or changed bytes, forwarding the original
// chunk cannot reconstruct the stream.
type StreamFilter interface {
	Filter(dir inspect.Direction, data []byte) ([]byte, error)
}

// reassemblySizer lets a codec raise the default inspector buffer to the
// largest packet its own parser admits. The codec remains the authority on
// the packet-specific cap; this only prevents the generic 8 MiB guard from
// rejecting a valid larger frame first.
type reassemblySizer interface {
	MaxReassemblyBytes() int
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

	// Metrics receives per-statement outcomes for a process-wide counter.
	// Optional; nil records nothing. Every connection's gate shares one
	// implementation, so it MUST be safe for concurrent use and MUST cost
	// no more than an atomic increment: it runs on the data path.
	Metrics Metrics
}

// Metrics is the counting hook a Gate reports into. Declared here as a
// narrow interface rather than imported, so the package that aggregates
// (analytics) depends on nothing and the gate depends on no aggregator.
type Metrics interface {
	// Statement records one judged statement. source is the kind of
	// evaluator that denied it — a policy.Verdict.Source, or one of the
	// gate's own Source* values — and empty when it was allowed.
	Statement(denied bool, source string)
	// Masked records values rewritten out of one response.
	Masked(values int)
	// AuditError records one audit event the sink could not write.
	AuditError()
}

// Denial sources the gate reports that no evaluator produced.
const (
	// SourceAudit is the fail-closed refusal when the audit sink is down.
	SourceAudit = "audit"
	// SourceStream is a codec refusing bytes it cannot safely forward.
	SourceStream = "stream"
)

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

	// DeniedStatement is the exact statement whose verdict stopped the
	// payload. Protocol denial writers use its wire metadata to correlate a
	// native error with the request. Nil for stream-level refusals that did
	// not decode a statement.
	DeniedStatement *inspect.Statement

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

	// clientFilter and serverFilter are optional pre-decode wire filters
	// discovered from their codecs. Duplex codecs may put the same filter in
	// both fields; each direction still owns its independent stream cursor.
	clientFilter StreamFilter
	serverFilter StreamFilter

	// mu guards the counters, which Close reads while a data-path goroutine
	// may still be incrementing them, and the exchange state below, which
	// the two pump goroutines of a byte-path gate write from opposite
	// directions. The inspectors are not guarded: they are per-direction
	// and each direction has one reader.
	mu         sync.Mutex
	statements int
	denied     int
	closed     bool

	// oneExchange says this gate lives exactly as long as one request and
	// the responses that answer it, which is true of a gate from
	// NewStatementGate (the gRPC lane builds one per RPC) and false of a
	// byte-path gate, which serves a whole connection.
	//
	// It gates skipResponses: the set of policy sources that, deciding a
	// request, declined its response side. On a one-exchange gate the set
	// simply lives as long as the gate. On a connection gate there is no
	// sound way to scope it: the codecs correlate responses to requests
	// (pgwire's extended protocol pipelines, MongoDB answers out of order
	// by requestID) but do not expose the pairing on the Statement, and a
	// direction flip is not a boundary once two requests are in flight —
	// request A's opt-out would silence OPA on request B's response. So a
	// connection gate records nothing and annotates the request instead.
	oneExchange   bool
	skipResponses map[string]bool
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
	maxBuffer := cfg.MaxBuffer
	if maxBuffer <= 0 {
		for _, codec := range []inspect.Codec{clientCodec, serverCodec} {
			if sized, ok := codec.(reassemblySizer); ok && sized.MaxReassemblyBytes() > maxBuffer {
				maxBuffer = sized.MaxReassemblyBytes()
			}
		}
	}
	if maxBuffer > 0 {
		client.SetMaxBuffer(maxBuffer)
		server.SetMaxBuffer(maxBuffer)
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
	if filter, ok := clientCodec.(StreamFilter); ok {
		g.clientFilter = filter
	}
	if filter, ok := serverCodec.(StreamFilter); ok {
		g.serverFilter = filter
	}
	// Discover the re-framing capability once, so the data path does not
	// type-assert per packet. A codec that cannot rebuild its own frames
	// leaves this nil and masks nothing; MaskSupported refuses the config.
	if rf, ok := server.Codec().(Reframer); ok {
		g.reframer = rf
		if activator, ok := rf.(rewriteActivator); ok && cfg.Masker != nil {
			activator.EnableRewrite()
		}
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
	return g.writeAudit(ctx, audit.SessionStartEvent(g.sess))
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

// FlushResponse returns any response bytes the codec is still holding after a
// normal response-stream completion.
//
// A re-framing codec buffers rows until their result set ends, because a row
// cannot be rebuilt once forwarded. If the connection closes mid-result-set
// those rows would be dropped, silently truncating the client's output, a
// worse failure than masking late. The relay MUST call this before a normal
// server pump stops. A denied or unsafe response must call DiscardResponse
// instead so held rows cannot follow the protocol error.
//
// Returns nil when nothing is held or the codec does not re-frame.
func (g *Gate) FlushResponse() []byte {
	if g.reframer == nil {
		return nil
	}
	if g.masker == nil {
		return g.reframer.Flush(nil)
	}
	// Rows masked here were held back by the codec and never passed through
	// maskByReframing, so this is their only chance to be counted.
	masked := 0
	out := g.reframer.Flush(func(column string, value []byte) []byte {
		res, _, n := g.masker.MaskCell(column, value)
		if n == 0 {
			return value
		}
		masked += n
		return res
	})
	g.countMasked(masked)
	return out
}

// DiscardResponse clears response bytes held by a re-framing codec without
// returning them. A response denial has already replaced the upstream result
// with a protocol error; releasing earlier rows after that error would leak
// denied data and corrupt the response stream.
func (g *Gate) DiscardResponse() {
	if g.reframer != nil {
		g.reframer.Flush(nil)
	}
}

func (g *Gate) inspect(ctx context.Context, dir inspect.Direction, data []byte) Decision {
	d := Decision{Allowed: true}

	filter := g.clientFilter
	if dir == inspect.FromServer {
		filter = g.serverFilter
	}
	if filter != nil && len(data) > 0 {
		filtered, err := filter.Filter(dir, data)
		if err != nil {
			d.Allowed = false
			d.Rule = "stream-filter"
			d.Message = err.Error()
			d.Err = fmt.Errorf("filter: %w", err)
			g.writeAudit(ctx, audit.ErrorEvent(g.sess, d.Err))
			g.mu.Lock()
			g.denied++
			g.mu.Unlock()
			g.countStatement(true, SourceStream)
			return d
		}
		data = filtered
	}
	d.Payload = data

	// A statement gate has no inspectors: its caller builds statements and
	// enters at EvaluateStatement. Refusing here beats a nil dereference,
	// and forwarding uninspected bytes is not an option this package offers.
	if g.client == nil {
		d.Allowed = false
		d.Rule = "gate"
		d.Message = "this gate evaluates statements, not bytes; use EvaluateStatement"
		d.Payload = nil
		return d
	}
	if len(data) == 0 {
		return d
	}

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
			g.countStatement(true, SourceStream)
			return d
		}
	}

	for _, stmt := range stmts {
		j := g.judge(ctx, stmt)
		if j.refusal != nil {
			r := *j.refusal
			r.Statements = stmts
			return r
		}
		if j.err != nil {
			d.Err = errors.Join(d.Err, j.err)
		}
		if j.denied {
			d.Allowed = false
			d.Message = j.message
			d.Rule = j.rule
			denied := stmt
			d.DeniedStatement = &denied
			d.Payload = nil // nothing may be forwarded
			return d
		}
	}

	// Masking is response-side only: rewriting a client's request would
	// change the statement the upstream executes, which breaks correctness
	// instead of protecting privacy.
	//
	// The codec does the rewriting, because only it knows the framing: a
	// Postgres DataRow carries its own lengths, an HTTP response its
	// Content-Length or chunk sizes, a WebSocket message its frames.
	// Substituting bytes in any of them desynchronizes the client. A codec
	// with no Reframer masks nothing here, and MaskSupported refuses the
	// config for it at load.
	if dir == inspect.FromServer && g.masker != nil && g.reframer != nil && len(data) > 0 {
		g.maskByReframing(ctx, &d, data)
	}

	return d
}

// judgment is the outcome of evaluating one statement: what the caller must
// join into its Decision, and whether it must stop.
type judgment struct {
	denied  bool
	rule    string
	message string

	// err carries the non-fatal failures (a failed audit write under
	// fail-open, an evaluator's infrastructure error) for logging.
	err error

	// refusal is non-nil when the audit trail is unavailable and the gate
	// is configured to fail closed. The caller returns it as-is after
	// filling Statements; nothing may be forwarded.
	refusal *Decision
}

// judge evaluates one statement: policy, counters, audit. It is the shared
// core of the byte path (inspect) and the statement path (EvaluateStatement),
// so the two cannot drift on the order that matters — audit BEFORE the
// caller forwards, because a crash between the write and the forward must
// not lose the record of the statement that ran.
func (g *Gate) judge(ctx context.Context, stmt inspect.Statement) judgment {
	verdict := g.evaluate(ctx, stmt)

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
	refused := auditErr != nil && g.cfg.FailOnAuditError

	// Counted once, after the audit result is known: a statement the policy
	// allowed but the fail-closed audit refused is a denial to the client,
	// and the counters must say what the client saw.
	denied := verdict.Denied || refused
	source := verdict.Source
	if refused {
		source = SourceAudit
	}
	g.mu.Lock()
	g.statements++
	if denied {
		g.denied++
	}
	g.mu.Unlock()
	g.countStatement(denied, source)

	if refused {
		denied := stmt
		return judgment{refusal: &Decision{
			Allowed:         false,
			Message:         "audit trail unavailable; statement refused",
			Rule:            "audit",
			DeniedStatement: &denied,
			Err:             auditErr,
		}}
	}

	return judgment{
		denied:  verdict.Denied,
		rule:    verdict.Rule,
		message: verdict.Message,
		err:     errors.Join(auditErr, verdict.Err),
	}
}

// NewStatementGate builds a Gate for a caller that constructs statements
// itself instead of handing over raw bytes: the gRPC lane, which terminates
// HTTP/2 in-process and holds parsed requests (ADR-0013), and later an
// ext_proc front end entering at the same point.
//
// No codecs are built and the registry is never consulted, so this works for
// a protocol with no codec — which is the point. Request and Response return
// an error Decision on such a gate; EvaluateStatement is its data path.
// Masking is the caller's job too: it holds the decoded values, and the
// Masker it needs is the same one Config carries.
func NewStatementGate(sess *session.Session, cfg Config) (*Gate, error) {
	if sess == nil {
		return nil, errors.New("sidecar/gate: nil session")
	}
	if cfg.Protocol == "" {
		cfg.Protocol = sess.Protocol
	}
	if cfg.Protocol == "" {
		return nil, errors.New("sidecar/gate: no protocol configured")
	}
	sess.Protocol = cfg.Protocol
	return &Gate{
		cfg:         cfg,
		sess:        sess,
		policy:      cfg.Policy,
		audit:       cfg.Audit,
		masker:      cfg.Masker,
		polCtx:      sess.PolicyContext(),
		oneExchange: true,
	}, nil
}

// EvaluateStatement judges one caller-built statement: policy, then audit,
// in that order, exactly as the byte path does. The returned Decision has no
// Payload; the caller owns the bytes and MUST NOT forward the unit this
// statement describes when Allowed is false.
//
// Safe on any Gate, but built for one from NewStatementGate.
func (g *Gate) EvaluateStatement(ctx context.Context, stmt inspect.Statement) Decision {
	stmts := []inspect.Statement{stmt}
	j := g.judge(ctx, stmt)
	if j.refusal != nil {
		r := *j.refusal
		r.Statements = stmts
		return r
	}
	d := Decision{Allowed: !j.denied, Statements: stmts, Err: j.err}
	if j.denied {
		d.Message = j.message
		d.Rule = j.rule
		denied := stmt
		d.DeniedStatement = &denied
	}
	return d
}

// Masker returns the masker this gate was configured with, nil when masking
// is off. The statement path needs it: a caller that decodes its own frames
// masks its own values, and reaching back into the Config it already handed
// over is how two copies drift.
func (g *Gate) Masker() Masker { return g.masker }

// RecordMasked writes the audit event for values a statement-level transport
// rewrote. Byte transports reach the same event through maskByReframing; a
// gRPC lane owns decoding and re-encoding itself, so it reports the result
// here after a successful proto marshal.
func (g *Gate) RecordMasked(ctx context.Context, entities []string, count int) error {
	if count == 0 {
		return nil
	}
	sort.Strings(entities)
	unique := entities[:0]
	for _, entity := range entities {
		if len(unique) == 0 || unique[len(unique)-1] != entity {
			unique = append(unique, entity)
		}
	}
	g.countMasked(count)
	err := g.writeAudit(ctx, audit.MaskedEvent(g.sess, unique, count))
	if g.cfg.FailOnAuditError {
		return err
	}
	return nil
}

// RecordActivity writes one non-statement record for this session: a
// capability admitted or refused, a terminal's geometry, a forward carried,
// a file transferred.
//
// It goes through the gate rather than straight to the sink so a lane has
// ONE place that knows about the session, the sink and the fail-closed
// policy. A lane that wrote to the sink itself would be a second audit path
// that could disagree with this one about whether an unrecorded event may
// still proceed.
//
// attrs is flat metadata and must never carry session content.
func (g *Gate) RecordActivity(ctx context.Context, activity string, attrs map[string]string) error {
	err := g.writeAudit(ctx, audit.ActivityEvent(g.sess, activity, attrs))
	if g.cfg.FailOnAuditError {
		return err
	}
	return nil
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
		d.Err = errors.Join(d.Err, err)
		g.writeAudit(ctx, audit.ErrorEvent(g.sess, err))

		// A reframer returning ErrStreamUnsafe has recognized a response it
		// cannot rebuild without leaking cleartext or losing packet
		// boundaries. Treat it like the same error from Decode: deny the
		// response so the proxy writes the protocol error and closes.
		if errors.Is(err, inspect.ErrStreamUnsafe) {
			d.Allowed = false
			d.Rule = "stream-unsafe"
			d.Message = err.Error()
			d.Payload = nil
			g.mu.Lock()
			g.denied++
			g.mu.Unlock()
			g.countStatement(true, SourceStream)
			return
		}

		// Other malformed responses retain the historical behavior: forward
		// what the codec produced and record the error. The upstream client
		// remains the authority on malformed protocol bytes.
	}

	d.Payload = out
	if res.Cells > 0 {
		sort.Strings(entities)
		d.Masked = entities
		d.MaskedCount = res.Cells
		g.countMasked(res.Cells)
		g.writeAudit(ctx, audit.MaskedEvent(g.sess, entities, res.Cells))
	}
}

// MaskSupported reports whether a protocol's response payload can be masked.
//
// The codec rebuilds its own frames around the new values: a Postgres
// DataRow, a MySQL or TDS row, an HTTP response body (Content-Length
// retagged, chunks re-chunked) or a WebSocket message. MaskSupported asks
// the codec for the Reframer capability instead of listing protocols, so a
// new re-framing codec does not require also remembering to edit this.
//
// Exported so a configuration layer can REFUSE masking on a protocol that
// cannot re-frame, instead of accepting the setting and silently never
// masking. One predicate, so the config check and the data path cannot drift.
func MaskSupported(p inspect.Protocol) bool {
	// SSH has no codec to ask. It rewrites a byte stream IN PLACE, with no
	// length header to correct and no frame to rebuild, which is safe for
	// exactly one reason — the replacement is the same size as what it
	// replaced. The daemon refuses every other mask strategy on an ssh lane
	// at load, and the lane fails the stream closed if a rewrite comes back
	// a different length. Without this branch inspect.New would be asked
	// for a protocol that has no decoder and masking would be refused on
	// the lane whose whole content path is masking.
	if p == inspect.SSH {
		return true
	}
	insp, err := inspect.New(p)
	if err != nil {
		return false
	}
	_, ok := insp.Codec().(Reframer)
	return ok
}

// evaluate runs the policy, defaulting to allow when none is configured.
//
// ctx rides on the evaluation context so an evaluator that waits (the
// analyzer's hold) stops when the connection ends.
func (g *Gate) evaluate(ctx context.Context, stmt inspect.Statement) policy.Verdict {
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
	ce, ok := g.policy.(policy.ContextualEvaluator)
	if !ok {
		return g.policy.Evaluate(stmt)
	}
	ec := &policy.EvalContext{Context: g.polCtx, ConnCtx: ctx}
	g.seedExchange(stmt, ec)
	v := ce.EvaluateWith(stmt, ec)
	g.recordExchange(stmt, ec, &v)
	return v
}

// seedExchange turns every source that declined this exchange's responses
// into a Requested veto on a response statement, which the source reads like
// any other veto.
func (g *Gate) seedExchange(stmt inspect.Statement, ec *policy.EvalContext) {
	if stmt.Direction != inspect.FromServer {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.skipResponses) == 0 {
		return
	}
	ec.Requested = make(map[string]bool, len(g.skipResponses))
	for source := range g.skipResponses {
		ec.Requested[source] = false
	}
}

// recordExchange keeps what a request decision declined, for the responses
// that follow it. Sticky for the gate's life: a later request statement of
// the same exchange (a client-streaming message) that says nothing does not
// undo what the first one said.
//
// On a connection gate the opt-out is not kept, for the reason on
// oneExchange, and the verdict says so: the request's audit record carries
// AnnotationResponsesUnscoped, so a Rego author reading "why is OPA still
// called on every row" finds the answer in the trail rather than in this
// file.
func (g *Gate) recordExchange(stmt inspect.Statement, ec *policy.EvalContext, v *policy.Verdict) {
	if stmt.Direction != inspect.FromClient || len(ec.SkipResponses) == 0 {
		return
	}
	if !g.oneExchange {
		if v.Annotations == nil {
			v.Annotations = make(map[string]string, 1)
		}
		v.Annotations[policy.AnnotationResponsesUnscoped] = "connection"
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.skipResponses == nil {
		g.skipResponses = make(map[string]bool, len(ec.SkipResponses))
	}
	for source := range ec.SkipResponses {
		g.skipResponses[source] = true
	}
}

// writeAudit records ev even when ctx has ended.
//
// ctx is the connection's, and a statement's record is written after its
// verdict: a hold that ended because the client left still owes the trail its
// outcome. A sink that honors cancellation (SQLite does) would drop exactly
// that record.
func (g *Gate) writeAudit(ctx context.Context, ev audit.Event) error {
	if g.audit == nil {
		return nil
	}
	err := g.audit.Write(context.WithoutCancel(ctx), ev)
	if err != nil && g.cfg.Metrics != nil {
		g.cfg.Metrics.AuditError()
	}
	return err
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
	return g.writeAudit(ctx, audit.SessionEndEvent(g.sess, statements, denied))
}

// Stats reports the running totals.
func (g *Gate) Stats() (statements, denied int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.statements, g.denied
}

// countStatement and countMasked forward to the configured Metrics. Two
// one-liners rather than a nil check at every site, so a new call site
// cannot forget the check.
func (g *Gate) countStatement(denied bool, source string) {
	if g.cfg.Metrics != nil {
		g.cfg.Metrics.Statement(denied, source)
	}
}

func (g *Gate) countMasked(values int) {
	if g.cfg.Metrics != nil {
		g.cfg.Metrics.Masked(values)
	}
}
