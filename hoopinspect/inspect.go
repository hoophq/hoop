// Package hoopinspect turns raw database wire-protocol bytes into structured
// statements, and structured statements into allow/deny verdicts.
//
// # Why this exists
//
// Envoy can already tell you that a Postgres client ran a DELETE against
// `customers.appdb`: `envoy.filters.network.postgres_proxy` parses Query and
// Parse messages and emits that pair as dynamic metadata for the RBAC filter.
// Within Postgres, at table-and-verb granularity, that is a real capability
// and this library does not pretend otherwise.
//
// It stops there. The metadata is a resource name and an operation verb, so a
// policy can say "no DELETE on customers" but not "no DELETE without a WHERE
// clause" or "no query touching the ssn column". Envoy's MySQL filter parses
// no SQL at all, and there is no SSH filter of any kind.
//
// hoopinspect fills exactly that gap and nothing else:
//
//   - The full statement text, not a summary of it.
//   - The same shape for every protocol, SQL and HTTP alike.
//   - A deny path that carries an operator-authored message back to the user,
//     rather than dropping the connection and leaving them to guess.
//
// # What it is not
//
// Not a proxy. It never opens a socket, never terminates TLS, never routes.
// It is a pure function over bytes you already have. Whatever holds the
// connection — Envoy, a sidecar, the hoop agent — keeps holding it.
//
// # Usage
//
//	insp := hoopinspect.New(hoopinspect.Postgres)
//	stmts, _ := insp.Inspect(hoopinspect.FromClient, packetBytes)
//	for _, s := range stmts {
//	    if v := policy.Evaluate(s); v.Denied {
//	        return errors.New(v.Message)
//	    }
//	}
//
// Inspect is safe to call with partial packets: it returns what it can decode
// and reports how many bytes it consumed, so a caller streaming a socket can
// retain the remainder. Inspectors are NOT safe for concurrent use; give each
// connection its own.
package hoopinspect

import (
	"errors"
	"fmt"
)

// Protocol identifies a wire protocol.
//
// Only the protocols with a shipped codec are listed. Adding one means adding
// a codec/<name> package that registers itself; a constant with no decoder
// behind it is a promise the library cannot keep, and a caller who passes it
// to New gets ErrUnsupportedProtocol at a confusing distance from the cause.
type Protocol string

const (
	Postgres Protocol = "postgres"
	HTTP     Protocol = "http"
)

// Direction says which side of the connection produced the bytes. Only
// FromClient currently yields statements; FromServer is accepted so callers
// can feed both halves of a stream through one code path without branching,
// and so response-side inspection can be added without an API change.
type Direction string

const (
	FromClient Direction = "client"
	FromServer Direction = "server"
)

// Operation is the normalized verb of a statement. It is derived from the
// statement text, not from the wire encoding, so it means the same thing
// across every protocol.
type Operation string

const (
	OpSelect   Operation = "select"
	OpInsert   Operation = "insert"
	OpUpdate   Operation = "update"
	OpDelete   Operation = "delete"
	OpCreate   Operation = "create"
	OpDrop     Operation = "drop"
	OpAlter    Operation = "alter"
	OpTruncate Operation = "truncate"
	OpGrant    Operation = "grant"
	OpRevoke   Operation = "revoke"
	OpCall     Operation = "call"
	OpShow     Operation = "show"
	OpSet      Operation = "set"
	OpBegin    Operation = "begin"
	OpCommit   Operation = "commit"
	OpRollback Operation = "rollback"

	// HTTP verbs. They are kept distinct from the SQL verbs rather than
	// mapped onto them (GET -> select, DELETE -> delete) because the mapping
	// is a lie in both directions: a POST to /search is a read, and a GET
	// with side effects is common in badly-behaved APIs. A policy author
	// should see the method the client actually sent.
	OpGet     Operation = "get"
	OpPost    Operation = "post"
	OpPut     Operation = "put"
	OpPatch   Operation = "patch"
	OpHead    Operation = "head"
	OpOptions Operation = "options"
	OpConnect Operation = "connect"
	OpTrace   Operation = "trace"

	// GraphQL operation types, resolved from the query document rather than
	// the HTTP method. Every GraphQL request is a POST, so the method alone
	// cannot distinguish a read from a write — this is the single biggest
	// blind spot in method-plus-path authorization.
	OpQuery        Operation = "query"
	OpMutation     Operation = "mutation"
	OpSubscription Operation = "subscription"

	OpOther   Operation = "other" // parsed, but not a verb we classify
	OpUnknown Operation = "unknown"
)

// Statement is one inspected unit of work: a SQL statement or an HTTP
// request/response. It is the input document a policy engine evaluates.
type Statement struct {
	// Protocol that produced this statement.
	Protocol Protocol `json:"protocol"`

	// Direction the bytes travelled.
	Direction Direction `json:"direction"`

	// Text is the statement verbatim, as the client sent it. This is the
	// field Envoy's postgres_proxy dynamic metadata does not give you.
	Text string `json:"text"`

	// Operation is the normalized verb (see Operation constants).
	Operation Operation `json:"operation"`

	// Tables lists the relations the statement references, lowercased and
	// deduplicated in order of appearance. Best effort: derived from FROM /
	// INTO / UPDATE / JOIN / TABLE keywords, not a full SQL grammar. Empty
	// when nothing was recognized — callers MUST NOT treat empty as "touches
	// nothing", only as "we could not tell".
	Tables []string `json:"tables,omitempty"`

	// Database is the target database when the protocol states it explicitly,
	// which for Postgres is only at login.
	Database string `json:"database,omitempty"`

	// HTTP is set only for the http protocol and carries the request/response
	// detail that has no SQL equivalent. Nil for every wire-database codec,
	// so a policy can branch on its presence.
	HTTP *HTTPDetail `json:"http,omitempty"`

	// Metadata carries protocol-specific details a policy may want: the
	// prepared-statement name for a Postgres Parse, the HTTP request method.
	// Keys are stable per protocol and documented on each codec.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// HTTPDetail is the HTTP-shaped half of a Statement.
//
// It exists because the useful policy questions about HTTP are not the ones
// you can ask about SQL. Envoy's ext_authz already gives OPA the method, path
// and headers of a REQUEST, and that is genuinely enough for "may alice call
// POST /admin". What it does not give you:
//
//   - The GraphQL operation. Every GraphQL call is POST /graphql, so method
//     and path cannot separate a read from a schema-mutating write. The
//     operation lives in the body.
//   - The RESPONSE. ext_authz is request-side; the filter decides before the
//     upstream is called. Response bodies are where the data actually leaves.
//   - A stable resource identity. /users/12345/orders and /users/67890/orders
//     are the same resource with different ids; a policy keyed on the raw
//     path needs a regex per endpoint.
type HTTPDetail struct {
	// Method is the request method, uppercased. Empty on a response.
	Method string `json:"method,omitempty"`

	// Path is the request path with the query string removed.
	Path string `json:"path,omitempty"`

	// Query holds the parsed query-string parameters. Repeated keys keep
	// every value.
	Query map[string][]string `json:"query,omitempty"`

	// Host is the request's Host header / :authority.
	Host string `json:"host,omitempty"`

	// Resource is Path with dynamic segments replaced by "*", so
	// /users/12345/orders becomes /users/*/orders. This is what a policy
	// should key on: it is stable across ids, and it collapses the
	// combinatorial explosion of per-id rules into one.
	//
	// Best effort. A segment is treated as dynamic when it is numeric, a
	// UUID, or a long opaque token. A slug like /users/alice is NOT
	// collapsed, because there is no way to tell it from a static segment.
	Resource string `json:"resource,omitempty"`

	// StatusCode is the response status. Zero on a request.
	StatusCode int `json:"status_code,omitempty"`

	// ContentType is the Content-Type header with parameters stripped
	// (e.g. "application/json", not "application/json; charset=utf-8").
	ContentType string `json:"content_type,omitempty"`

	// Headers carries the headers a policy is allowed to see. It is
	// populated only for the header names the codec was configured to
	// expose, because forwarding every header to a policy engine is how
	// Authorization tokens end up in decision logs.
	Headers map[string]string `json:"headers,omitempty"`

	// Body is the request or response body, truncated to the codec's
	// MaxBodyBytes. Empty when the codec was configured not to capture it.
	Body string `json:"body,omitempty"`

	// BodyTruncated reports that Body holds only a prefix. A policy matching
	// on body content MUST treat a truncated body as inconclusive rather
	// than as proof a pattern is absent.
	BodyTruncated bool `json:"body_truncated,omitempty"`

	// GraphQL is set when the body parsed as a GraphQL document.
	GraphQL *GraphQLDetail `json:"graphql,omitempty"`
}

// GraphQLDetail describes a parsed GraphQL request.
//
// This is the field that makes HTTP inspection worth doing. `POST /graphql`
// is indistinguishable from any other POST at the ext_authz layer, so a
// method-and-path policy either allows every GraphQL operation or none.
type GraphQLDetail struct {
	// OperationType is query, mutation or subscription.
	OperationType Operation `json:"operation_type"`

	// OperationName is the client-supplied name, when present.
	OperationName string `json:"operation_name,omitempty"`

	// RootFields lists the top-level selections of the operation — the
	// actual resolvers being invoked. For a mutation these are the write
	// operations: ["deleteUser", "resetPassword"].
	RootFields []string `json:"root_fields,omitempty"`

	// Depth is the maximum nesting depth of the selection set. Deeply nested
	// queries are the standard GraphQL denial-of-service vector, and a depth
	// limit is a policy every GraphQL deployment eventually needs.
	Depth int `json:"depth,omitempty"`
}

// String renders the statement for logs. Long text is elided so a log line
// cannot be blown out by a multi-megabyte INSERT.
func (s Statement) String() string {
	text := s.Text
	if len(text) > 120 {
		text = text[:117] + "..."
	}
	return fmt.Sprintf("%s/%s %s %q", s.Protocol, s.Direction, s.Operation, text)
}

// Codec decodes one wire protocol. Implementations live in codec/<name> and
// are registered with New.
type Codec interface {
	// Protocol reports which protocol this codec decodes.
	Protocol() Protocol

	// Decode parses as many complete messages as `data` contains and returns
	// the statements found, plus the number of bytes consumed. A caller
	// streaming a socket retains data[n:] and prepends it to the next read.
	//
	// Decode MUST NOT return an error for a merely incomplete buffer: it
	// returns the statements it could decode and a consumed count that stops
	// at the partial message. An error means the bytes are malformed for this
	// protocol, and the caller should stop trusting the stream.
	Decode(dir Direction, data []byte) (stmts []Statement, consumed int, err error)
}

// ErrUnsupportedProtocol is returned by New for a protocol with no codec.
var ErrUnsupportedProtocol = errors.New("hoopinspect: unsupported protocol")

// Inspector decodes a byte stream for one connection.
//
// It buffers across calls: a statement split over two TCP segments is
// reassembled. An Inspector is stateful and NOT safe for concurrent use — one
// per connection.
type Inspector struct {
	codec Codec

	// buf holds bytes from previous calls that did not form a complete
	// message. Bounded by maxBuffer.
	buf []byte

	// maxBuffer caps the reassembly buffer. A peer that opens a message
	// header claiming a huge length and then stalls would otherwise pin
	// memory for the life of the connection. On overflow Inspect returns
	// ErrBufferOverflow and the caller should close the connection.
	maxBuffer int
}

// DefaultMaxBuffer bounds per-connection reassembly. Postgres allows a single
// message up to 2^31 bytes, so this is a policy choice, not a protocol
// limit: 8 MiB comfortably holds any statement a human
// or ORM emits while refusing to buffer a bulk COPY into RAM.
const DefaultMaxBuffer = 8 << 20

// ErrBufferOverflow means a single message exceeded the inspector's reassembly
// budget. The stream cannot be resynchronized; close the connection.
var ErrBufferOverflow = errors.New("hoopinspect: message exceeds max buffer")

// New returns an Inspector for the given protocol. It returns
// ErrUnsupportedProtocol if no codec is registered.
//
// Codecs register themselves via Register in their package init, so importing
// codec/postgres is what makes Postgres available. A caller that wants every
// protocol imports the umbrella package `codec/all`.
func New(p Protocol) (*Inspector, error) {
	newCodec, ok := lookup(p)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedProtocol, p)
	}
	// A fresh codec per Inspector: stateful codecs must not be shared across
	// connections. See Register.
	return &Inspector{codec: newCodec(), maxBuffer: DefaultMaxBuffer}, nil
}

// NewWithCodec returns an Inspector driving a caller-supplied codec. Use it to
// inspect a protocol this library does not ship, or to wrap a codec with
// instrumentation, without touching the registry.
func NewWithCodec(c Codec) *Inspector {
	return &Inspector{codec: c, maxBuffer: DefaultMaxBuffer}
}

// SetMaxBuffer overrides DefaultMaxBuffer. A value <= 0 restores the default.
func (i *Inspector) SetMaxBuffer(n int) {
	if n <= 0 {
		n = DefaultMaxBuffer
	}
	i.maxBuffer = n
}

// Protocol reports the protocol being inspected.
func (i *Inspector) Protocol() Protocol { return i.codec.Protocol() }

// Inspect feeds one chunk of the stream and returns the statements it
// completes. Bytes belonging to a partial trailing message are retained for
// the next call.
//
// Passing an empty slice is a no-op that flushes nothing: incomplete messages
// stay buffered, because a statement is only a statement once it is whole.
func (i *Inspector) Inspect(dir Direction, data []byte) ([]Statement, error) {
	if len(data) == 0 && len(i.buf) == 0 {
		return nil, nil
	}

	// Fast path: nothing buffered, so decode straight from the caller's slice
	// and only copy the remainder. This is the common case (one packet, one
	// or more complete messages) and it avoids an allocation per call.
	input := data
	if len(i.buf) > 0 {
		input = append(i.buf, data...)
	}

	if len(input) > i.maxBuffer {
		// Only an error if we cannot make progress. A large input that
		// decodes fully is fine; a large input that is still one partial
		// message is the pathological case below.
		stmts, consumed, err := i.codec.Decode(dir, input)
		if err != nil {
			i.buf = nil
			return stmts, err
		}
		if consumed == 0 {
			i.buf = nil
			return stmts, ErrBufferOverflow
		}
		i.retain(input, consumed)
		return stmts, nil
	}

	stmts, consumed, err := i.codec.Decode(dir, input)
	if err != nil {
		// A malformed stream is unrecoverable: drop the buffer so a caller
		// that ignores the error does not keep re-decoding the same garbage.
		i.buf = nil
		return stmts, err
	}
	i.retain(input, consumed)
	return stmts, nil
}

// retain stores the undecoded tail of input for the next Inspect call.
func (i *Inspector) retain(input []byte, consumed int) {
	if consumed >= len(input) {
		i.buf = nil
		return
	}
	rest := input[consumed:]
	// Copy: input may alias the caller's buffer, which they are free to reuse
	// the moment Inspect returns.
	i.buf = make([]byte, len(rest))
	copy(i.buf, rest)
}

// Buffered reports how many bytes are held pending more input. Useful for
// metrics and for asserting in tests that a stream ended cleanly.
func (i *Inspector) Buffered() int { return len(i.buf) }

// Reset discards buffered bytes. Call it when reusing an Inspector for a new
// connection.
func (i *Inspector) Reset() { i.buf = nil }
