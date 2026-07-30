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
