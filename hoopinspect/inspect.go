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
// hoopinspect fills that gap:
//
//   - The full statement text, not a summary of it.
//   - The same shape for every protocol, SQL and HTTP alike.
//   - A deny path that carries an operator-authored message back to the user,
//     rather than dropping the connection and leaving them to guess.
//
// # Boundaries
//
// hoopinspect is a pure function over bytes you already have. It opens no
// socket, terminates no TLS, and routes nothing. Whatever holds the
// connection (Envoy, a sidecar, the hoop agent) keeps holding it.
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
// and reports how many bytes it consumed, so you can retain the remainder
// while streaming a socket. Inspectors are NOT safe for concurrent use; give
// each connection its own.
package hoopinspect

import (
	"errors"
	"fmt"
)

// Codec decodes one wire protocol. Implementations live in codec/<name> and
// are registered with New.
type Codec interface {
	// Protocol reports which protocol this codec decodes.
	Protocol() Protocol

	// Decode parses as many complete messages as `data` contains and returns
	// the statements found, plus the number of bytes consumed. When streaming
	// a socket, retain data[n:] and prepend it to the next read.
	//
	// Decode MUST NOT return an error for an incomplete buffer: it returns
	// the statements it could decode and a consumed count that stops at the
	// partial message. An error means the bytes are malformed for this
	// protocol, so stop trusting the stream.
	Decode(dir Direction, data []byte) (stmts []Statement, consumed int, err error)
}

// ErrUnsupportedProtocol is returned by New for a protocol with no codec.
var ErrUnsupportedProtocol = errors.New("hoopinspect: unsupported protocol")

// Inspector decodes a byte stream for one connection.
//
// It buffers across calls: a statement split over two TCP segments is
// reassembled. An Inspector is stateful and NOT safe for concurrent use. Give
// each connection its own.
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
// message up to 2^31 bytes, so this is a policy choice rather than a protocol
// limit: 8 MiB holds any statement a human or ORM emits while refusing to
// buffer a bulk COPY into RAM.
const DefaultMaxBuffer = 8 << 20

// ErrBufferOverflow means a single message exceeded the inspector's reassembly
// budget. The stream cannot be resynchronized; close the connection.
var ErrBufferOverflow = errors.New("hoopinspect: message exceeds max buffer")

// New returns an Inspector for the given protocol. It returns
// ErrUnsupportedProtocol if no codec is registered.
//
// Codecs register themselves via Register in their package init, so importing
// codec/postgres makes Postgres available. Import the umbrella package
// `codec/all` to get every shipped protocol.
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

// Codec returns the codec driving this Inspector.
//
// It exists so you can type-assert for an OPTIONAL capability a codec offers
// beyond the Codec interface (response re-framing, for instance) instead of
// the library growing a method every codec must stub out.
func (i *Inspector) Codec() Codec { return i.codec }

// Inspect feeds one chunk of the stream and returns the statements it
// completes. Bytes belonging to a partial trailing message are retained for
// the next call.
//
// Passing an empty slice is a no-op that flushes nothing: incomplete messages
// stay buffered, because a message is decoded only once it is complete.
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
