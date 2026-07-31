package proxy

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"

	"github.com/hoophq/hoopinspect"
)

// Postgres negotiates TLS in-band rather than by connecting to a TLS port.
// The client sends this 8-byte packet before anything else and waits for a
// one-byte reply: 'S' to proceed with a handshake, 'N' to continue in the
// clear.
//
//	int32  8         length, including itself
//	int32  80877103  the SSLRequest sentinel (1234 << 16 | 5679)
//
// https://www.postgresql.org/docs/current/protocol-flow.html#PROTOCOL-FLOW-SSL
const (
	pgSSLRequestCode uint32 = 80877103
	pgSSLRequestLen  int32  = 8
)

// startTLS wraps conn in TLS using the negotiation the protocol requires.
//
// # Why this is not just tls.Client
//
// A TLS-on-connect dial (tls.DialWithDialer) sends a ClientHello as the first
// bytes on the wire. Postgres expects an SSLRequest packet there instead, so
// the server reads a handshake record where a startup packet should be and
// drops the connection. The symptom is "server closed the connection
// unexpectedly" on the client and this in the server log:
//
//	received direct SSL connection request without ALPN protocol negotiation
//
// So the negotiation is chosen per protocol, and a protocol whose TLS starts
// immediately (HTTP) gets the plain handshake.
//
// The returned conn is a *tls.Conn: reads yield DECRYPTED bytes. That is the
// property the gate depends on. TLS protects the hop to the upstream; it does
// not hide the payload from the inspector, which is the whole point of
// terminating it here rather than tunneling through.
func startTLS(conn net.Conn, addr string, proto hoopinspect.Protocol, cfg *tls.Config) (net.Conn, error) {
	if proto == hoopinspect.Postgres {
		if err := negotiatePostgresTLS(conn); err != nil {
			return nil, err
		}
	}

	// tls.Dial infers ServerName from the dial address; tls.Client does not,
	// and an empty ServerName with verification on fails every handshake.
	// Fill it the same way so an operator who set no server_name gets the
	// behavior a plain TLS dial would have given them.
	//
	// It comes from the CONFIGURED address, never conn.RemoteAddr(), which is
	// the resolved IP: a certificate issued for "appdb" does not match
	// "172.18.0.5", so using the peer address would fail every verified
	// handshake against a DNS-named upstream.
	if cfg.ServerName == "" && !cfg.InsecureSkipVerify {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("upstream TLS: cannot derive server name from %q: %w", addr, err)
		}
		cfg = cfg.Clone()
		cfg.ServerName = host
	}

	tc := tls.Client(conn, cfg)
	if err := tc.Handshake(); err != nil {
		return nil, fmt.Errorf("upstream TLS handshake: %w", err)
	}
	return tc, nil
}

// negotiatePostgresTLS performs the SSLRequest exchange on a freshly dialled
// connection, leaving it positioned for a TLS handshake.
//
// A refusal ('N') is an error rather than a silent downgrade to plaintext.
// An operator who configured upstream_tls asked for an encrypted hop, and
// quietly sending credentials in the clear because the server said no is the
// failure they were trying to prevent.
func negotiatePostgresTLS(conn net.Conn) error {
	var req [8]byte
	binary.BigEndian.PutUint32(req[0:4], uint32(pgSSLRequestLen))
	binary.BigEndian.PutUint32(req[4:8], pgSSLRequestCode)
	if _, err := conn.Write(req[:]); err != nil {
		return fmt.Errorf("postgres SSLRequest: %w", err)
	}

	var reply [1]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return fmt.Errorf("postgres SSLRequest reply: %w", err)
	}

	switch reply[0] {
	case 'S':
		return nil
	case 'N':
		return fmt.Errorf("postgres upstream refused TLS (replied 'N'): " +
			"the server has ssl=off, or hba rules deny it for this client; " +
			"enable TLS on the server or remove upstream_tls from this listener")
	case 'E':
		// The server can answer an SSLRequest with an ErrorResponse when it
		// is too old to know the packet. Naming that explicitly saves the
		// next reader from decoding a stray 'E' by hand.
		return fmt.Errorf("postgres upstream rejected the SSLRequest with an error " +
			"response; the server may predate SSLRequest support")
	default:
		return fmt.Errorf("postgres upstream sent an unexpected SSLRequest reply %q", reply[0])
	}
}

// AuthenticationSASL is the server's list of offered SASL mechanisms:
//
//	byte1  'R'
//	int32  length
//	int32  10  (the AuthenticationSASL selector)
//	then NUL-terminated mechanism names, ended by an extra NUL
const (
	pgTagAuth             byte   = 'R'
	pgAuthSASL            uint32 = 10
	pgSCRAMChannelBinding        = "SCRAM-SHA-256-PLUS"
)

// stripChannelBinding removes SCRAM-SHA-256-PLUS from an AuthenticationSASL
// message, returning the rewritten buffer and whether anything changed.
//
// # Why the offer cannot be relayed
//
// Channel binding ties the SCRAM exchange to the TLS channel the two parties
// share. When the relay terminates upstream TLS there is no single channel:
// the server binds to its session with the relay, and the client, which has
// its own connection, cannot produce a matching binding. The exchange fails
// by design, and that is the mechanism working correctly.
//
// Relaying the offer anyway is worse than useless. libpq refuses a "-PLUS"
// mechanism offered over a connection it knows is not encrypted, because that
// pairing is exactly what a downgrade attack looks like, and it fails with
//
//	server offered SCRAM-SHA-256-PLUS authentication over a non-SSL connection
//
// before the user ever runs a query. Dropping the mechanism leaves plain
// SCRAM-SHA-256, which authenticates the same password against the same
// verifier and works across the hop.
//
// The rewrite is length-corrected: the message declares its own size, so
// removing bytes without fixing the header desynchronizes the client.
func stripChannelBinding(data []byte) ([]byte, bool) {
	// 1 tag + 4 length + 4 selector
	const headerLen = 9
	if len(data) < headerLen || data[0] != pgTagAuth {
		return data, false
	}
	length := binary.BigEndian.Uint32(data[1:5])
	// length counts itself but not the tag, so the message spans 1+length.
	total := int(length) + 1
	if length < 8 || total > len(data) {
		return data, false // not fully buffered, or not a sane length
	}
	if binary.BigEndian.Uint32(data[5:9]) != pgAuthSASL {
		return data, false
	}

	body := data[headerLen:total]
	want := append([]byte(pgSCRAMChannelBinding), 0)
	idx := bytes.Index(body, want)
	if idx < 0 {
		return data, false
	}

	// Rebuild: everything before the mechanism, everything after it, with the
	// length field reduced by exactly what was removed.
	out := make([]byte, 0, len(data)-len(want))
	out = append(out, data[:headerLen]...)
	out = append(out, body[:idx]...)
	out = append(out, body[idx+len(want):]...)
	out = append(out, data[total:]...) // any pipelined messages after it
	binary.BigEndian.PutUint32(out[1:5], length-uint32(len(want)))
	return out, true
}

// maxSASLFrame bounds what saslReassembler will hold for a split
// AuthenticationSASL message.
//
// A real offer is well under a hundred bytes: two mechanism names and their
// terminators. The cap is what stops a server that sends 'R' followed by a
// 4 GiB length from making the relay buffer until it dies -- a frame the
// reassembler will not wait for is forwarded immediately instead.
const maxSASLFrame = 8 << 10

// saslReassembler rebuilds a split AuthenticationSASL message so
// stripChannelBinding is handed a whole frame.
//
// # Why pump cannot just call the strip function
//
// stripChannelBinding refuses to rewrite a fragment, and it is right to: the
// message declares its own size, so shrinking the length field of a partial
// frame points the client at a boundary that is not there and desynchronizes
// the rest of the connection. But pump feeds it whatever one Read returned,
// and TCP delivers bytes, not messages. The 43-byte offer usually arrives
// whole; when a TLS record boundary lands inside it the strip silently does
// nothing, the -PLUS mechanism reaches a client that knows its own
// connection is plaintext, and libpq fails the login before a query runs.
// Intermittently, which is the worst way to find out.
//
// # Why the hold cannot outlast authentication
//
// Only the first server->client frame is ever held. AuthenticationSASL is
// the first message a Postgres server sends, so once one complete frame has
// passed there is no offer still coming, and every later byte is forwarded
// untouched. Buffering past that point would put reassembly on the path of
// every result set.
type saslReassembler struct {
	pending []byte
	done    bool
}

// feed takes one read from the server and returns the bytes to forward, plus
// whether the mechanism was stripped from them.
//
// An empty return means the chunk was a partial authentication frame and is
// being held; the caller forwards nothing and reads again.
func (r *saslReassembler) feed(chunk []byte) (out []byte, stripped bool) {
	if r.done || len(chunk) == 0 {
		return chunk, false
	}

	data := chunk
	if len(r.pending) > 0 {
		r.pending = append(r.pending, chunk...)
		data = r.pending
	}

	// Every authentication message carries the 'R' tag. Anything else means
	// the server is past authentication or never began it.
	if data[0] != pgTagAuth {
		return r.release(data), false
	}
	if len(data) < 5 {
		return r.hold(data), false
	}

	// length counts itself but not the tag, so the message spans 1+length.
	length := binary.BigEndian.Uint32(data[1:5])
	total := int(length) + 1
	if length < 8 || total > maxSASLFrame {
		// Not a frame worth waiting for. Forward it and let the client's own
		// parser be the authority on its protocol.
		return r.release(data), false
	}
	if total > len(data) {
		return r.hold(data), false
	}

	// A complete authentication frame, so no SASL offer can still be in
	// flight: this is the last chunk the reassembler inspects, whether or not
	// it turned out to be the offer.
	rewritten, changed := stripChannelBinding(data)
	return r.release(rewritten), changed
}

// hold keeps a partial frame until the rest of it arrives.
func (r *saslReassembler) hold(data []byte) []byte {
	if len(r.pending) == 0 {
		// data aliases pump's read buffer, which the next Read overwrites.
		r.pending = append([]byte(nil), data...)
	}
	return nil
}

// release forwards data and retires the reassembler.
func (r *saslReassembler) release(data []byte) []byte {
	r.done = true
	r.pending = nil
	return data
}
