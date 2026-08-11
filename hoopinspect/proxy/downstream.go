package proxy

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/hoophq/hoopinspect"
)

// Postgres sends three untagged packets before normal message flow, each an
// int32 length followed by an int32 code.
//
// https://www.postgresql.org/docs/current/protocol-message-formats.html
const (
	pgGSSEncRequestCode uint32 = 80877104
	pgCancelRequestCode uint32 = 80877102
	pgNegotiateLen             = 8
)

// maxNegotiationRounds bounds the pre-startup exchange.
//
// A well-behaved client sends at most a GSSENCRequest and an SSLRequest before
// its StartupMessage. One that loops forever asking to encrypt would otherwise
// hold a goroutine and a socket without ever authenticating.
const maxNegotiationRounds = 4

// negotiateDownstream runs the pgwire pre-startup exchange as the SERVER side,
// returning a connection positioned at the client's StartupMessage.
//
// # Why the relay answers these itself
//
// Both packets ask to wrap the whole session in something the relay cannot
// read, so the answer decides whether this lane can enforce anything at all.
//
// GSSENCRequest is the dangerous one, and it is the DEFAULT for psql: libpq
// sets gssencmode=prefer, so any developer holding a Kerberos ticket asks for
// a GSS-wrapped transport before anything else, ahead of TLS. If the server
// accepts, every later byte is ciphertext, the gate sees no statements, and
// nothing reports that inspection stopped. Refusing costs the client nothing:
// it falls back and KEEPS its Kerberos authentication, which travels as
// ordinary tagged messages the codec already forwards untouched.
//
// SSLRequest is answered 'S' when a certificate is configured, so the client's
// leg is encrypted and the relay still reads plaintext. With no certificate it
// is answered 'N' and the client decides whether to proceed.
//
// # Why 'N' and never 'E'
//
// Both refuse, but 'N' keeps the socket. pgjdbc closes and REOPENS the TCP
// connection on 'E', which doubles every login in the audit trail and makes a
// normal session look like a reconnect loop.
//
// # Why the client always speaks first here
//
// pgwire has no server greeting: the backend sends nothing until it has read a
// startup packet. Blocking for the client's first bytes is therefore the
// protocol's own ordering, not an assumption imposed on it.
// It also returns the `user` the client named in its StartupMessage, which the
// caller records as the session principal. See startupUser for what that name
// is worth.
func negotiateDownstream(
	conn net.Conn,
	proto hoopinspect.Protocol,
	tlsCfg *tls.Config,
	timeout time.Duration,
) (net.Conn, string, error) {
	if proto != hoopinspect.Postgres {
		// Only pgwire negotiates in-band. TDS 8.0 is TLS-on-connect, which is
		// for whatever fronts this relay to terminate, and HTTP has no
		// equivalent exchange.
		return conn, "", nil
	}

	if timeout > 0 {
		if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
			return nil, "", err
		}
	}

	// Buffered so classification does not fragment the stream. Peek fills the
	// buffer with everything one socket read returns, and the gate's first
	// Read then gets the whole chunk. Consuming 8 bytes raw instead would hand
	// the gate a partial message, which it would forward before it could
	// judge it — the prefix of a statement reaching the upstream ahead of the
	// verdict.
	br := bufio.NewReaderSize(conn, 16<<10)

	for range maxNegotiationRounds {
		hdr, err := br.Peek(pgNegotiateLen)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, "", fmt.Errorf("client closed during negotiation: %w", err)
			}
			return nil, "", fmt.Errorf("reading the client's first packet: %w", err)
		}

		length := binary.BigEndian.Uint32(hdr[0:4])
		code := binary.BigEndian.Uint32(hdr[4:8])

		// Every negotiation packet is exactly 8 bytes and self-describing.
		// Anything else is the StartupMessage, so it belongs to the caller and
		// stays in the buffer unread.
		if length != pgNegotiateLen {
			return finishNegotiation(conn, br, timeout, startupUser(br, length))
		}

		switch code {
		case pgGSSEncRequestCode:
			if _, err := br.Discard(pgNegotiateLen); err != nil {
				return nil, "", err
			}
			if _, err := conn.Write([]byte{'N'}); err != nil {
				return nil, "", fmt.Errorf("refusing GSS encryption: %w", err)
			}

		case pgSSLRequestCode:
			if _, err := br.Discard(pgNegotiateLen); err != nil {
				return nil, "", err
			}
			if tlsCfg == nil {
				if _, err := conn.Write([]byte{'N'}); err != nil {
					return nil, "", fmt.Errorf("refusing SSL: %w", err)
				}
				continue
			}
			if _, err := conn.Write([]byte{'S'}); err != nil {
				return nil, "", fmt.Errorf("accepting SSL: %w", err)
			}
			// Anything still buffered here would be a client that spoke
			// before our 'S' reached it, which no client does and a hostile
			// one could use to smuggle plaintext into the TLS session.
			if br.Buffered() > 0 {
				return nil, "", errors.New("client sent data before the TLS handshake")
			}
			tc := tls.Server(conn, tlsCfg)
			if err := tc.Handshake(); err != nil {
				return nil, "", fmt.Errorf("downstream TLS handshake: %w", err)
			}
			// The exchange restarts inside TLS: the client sends its startup
			// packet, and may legitimately negotiate again first.
			conn = tc
			br = bufio.NewReaderSize(tc, 16<<10)

		case pgCancelRequestCode:
			// A cancel carries no session. Hand it through; the upstream
			// answers it and closes.
			return finishNegotiation(conn, br, timeout, "")

		default:
			return finishNegotiation(conn, br, timeout, "")
		}
	}

	return nil, "", errors.New("client kept renegotiating without sending a startup packet")
}

// maxStartupPacket bounds how much of a StartupMessage is inspected for the
// user parameter. Postgres itself refuses a startup packet above 10000 bytes.
const maxStartupPacket = 10000

// startupUser reads the `user` parameter out of the StartupMessage waiting in
// br, WITHOUT consuming it: the gate still needs the packet whole.
//
// # What this name is worth
//
// At this instant it is a CLAIM. The client has asserted who it wants to be
// and has proved nothing. It becomes true when the backend answers
// AuthenticationOk, because Postgres validated the credential against this
// exact name, and under `gss` that means a Kerberos ticket for this principal.
//
// The audit trail resolves the difference on its own: statements only flow
// after authentication succeeds, so every statement attributed to a principal
// carries a verified one. A session that shows a principal and no statements
// is a login that failed, and the name on it is what the client wanted rather
// than what it proved.
//
// Returns "" when the packet is not a v3 StartupMessage (a cancel request, or
// a protocol version this relay does not recognize) or carries no user.
func startupUser(br *bufio.Reader, length uint32) string {
	if length < 9 || length > maxStartupPacket {
		return ""
	}
	pkt, err := br.Peek(int(length))
	if err != nil {
		return ""
	}
	// Only protocol 3.x lays out parameters this way.
	if binary.BigEndian.Uint32(pkt[4:8])>>16 != 3 {
		return ""
	}

	// NUL-terminated key/value pairs, ending with an empty key.
	params := pkt[8:]
	for {
		k, rest, ok := bytes.Cut(params, []byte{0})
		if !ok || len(k) == 0 {
			return ""
		}
		v, rest, ok := bytes.Cut(rest, []byte{0})
		if !ok {
			return ""
		}
		if string(k) == "user" {
			return string(v)
		}
		params = rest
	}
}

// finishNegotiation clears the handshake deadline and hands back a connection
// that reads through the buffer the negotiation filled.
func finishNegotiation(
	conn net.Conn, br *bufio.Reader, timeout time.Duration, user string,
) (net.Conn, string, error) {
	if timeout > 0 {
		// The relay's own IdleTimeout governs from here; leaving the
		// handshake deadline set would kill a long-running query.
		if err := conn.SetDeadline(time.Time{}); err != nil {
			return nil, "", err
		}
	}
	return &bufferedConn{Conn: conn, r: br}, user, nil
}

// bufferedConn reads through a bufio.Reader while keeping the underlying
// connection's writes, deadlines and addresses.
//
// The negotiation has to look at the first 8 bytes to classify a connection.
// Those bytes are part of the StartupMessage when it is not a negotiation
// packet, so they must still reach the gate — and reach it together with
// whatever followed them in the same segment.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) { return c.r.Read(b) }
