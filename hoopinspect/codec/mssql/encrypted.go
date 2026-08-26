package mssql

import (
	"encoding/binary"
	"fmt"

	"github.com/hoophq/hoopinspect"
)

// Login-only encryption, and how this codec survives it.
//
// # On the wire
//
// PRELOGIN's ENCRYPTION option has four values, and only one of them means
// "no TLS":
//
//	0x00 ENCRYPT_OFF      encryption available, off -- BUT the login is still encrypted
//	0x01 ENCRYPT_ON       the whole session is encrypted
//	0x02 ENCRYPT_NOT_SUP  no encryption anywhere
//	0x03 ENCRYPT_REQ      the server requires encryption
//
// ENCRYPT_OFF is the trap. You read it as "encryption off"; it means "encrypt
// the login only". A SQL Server with TLS administratively disabled still
// negotiates it: with no certificate installed the server mints a self-signed
// one at startup and encrypts credentials with that, so an operator who
// turned encryption off still gets an encrypted login. Microsoft specified
// this, so anyone hunting a misconfiguration here finds none.
//
// Every Go client hits it. go-mssqldb defaults to ENCRYPT_OFF
// (msdsn/conn_str.go:297, `var encryption Encryption = EncryptionOff`), so
// any caller that leaves `encrypt=` unset takes this path. Only the modern
// Microsoft drivers skip it, by defaulting to Mandatory and encrypting
// everything.
//
// # Exactly one PACKET is encrypted, not one message
//
// MS-TDS 3.2.5.3: "the first TDS packet of the Login message MUST be
// encrypted using TLS/SSL and encapsulated in a TLS/SSL message. All other
// TDS packets sent or received MUST be in plaintext."
//
// Packet, not message. A client splits a LOGIN7 larger than the negotiated
// packet size (4096 by default) and encrypts only the first fragment; it puts
// the rest, credentials included, in the clear. A decoder that waited for the
// whole LOGIN7 message would run straight past the boundary, so this one
// walks records and stops at the first byte that is not one.
//
// go-mssqldb throws the switch in one field assignment covering both
// directions (tds.go:1262, `outbuf.afterFirst = func() { outbuf.transport =
// toconn }`, fired by buf.go:99 after the first packet flushes), so neither
// side can keep sending ciphertext past it.
//
// # Where the ciphertext sits
//
// The handshake rides inside 0x12 PRELOGIN packets, which the decoder already
// forwards untouched. The bytes after it break the walk: having finished the
// handshake, the client inverts the nesting from TLS-inside-TDS to
// TDS-inside-TLS and writes records to the socket raw, with no TDS header.
// libhoop's own client does exactly this (agent/mssql/net.go, where
// tlsHandshakeConn stops wrapping once `upgraded` is set).
//
// The decoder then reads a record as a packet header and misparses it. Given
// the application-data record `17 03 03 04 00` it takes 0x17 for the type and
// BigEndian.Uint16(b[2:4]) = 0x0304 = 772 for the length, resumes 772 bytes
// into ciphertext, and gets every offset after that wrong. The socket keeps
// carrying traffic while the relay stops understanding it: no statements
// decoded, the gate forwarding bytes it could not parse, policy, masking and
// the audit trail all ending, and one error per read as the only signal.
//
// The two directions are asymmetric. A client emits one raw region, the
// encrypted login packet. A server under ENCRYPT_OFF emits none: it keeps its
// whole handshake inside 0x12 (MS-TDS 3.3.5.2, or 0x04 from older endpoints)
// and sends its login response in the clear, so the routing-ENVCHANGE guard
// in decodeServer keeps working through an encrypted login. decodeServer runs
// the same walk anyway, as a safety net over a path that carries no known
// traffic.
//
// # The walk lands on the exact byte
//
// A TLS record declares its own length: 5-byte header, payload length in the
// last two bytes, big-endian. Following that framing consumes the region
// record by record and finishes on the byte where plaintext resumes. This
// codec decrypts nothing and searches for nothing; it reads the length the
// sender wrote.
//
// Nothing can confuse the two framings. TDS packet types run 0x01-0x12 and
// TLS content types 0x14-0x17, disjoint sets, and a record header must also
// carry a 0x03 version major and a plausible length. The entry test is a
// structural check.
//
// One record per packet is usual, not guaranteed: go-mssqldb forces
// DynamicRecordSizingDisabled, giving 16384-byte records, and a negotiated
// TDS packet size above that (TDS permits 32767) splits one packet across
// two. The walk handles any number of records, so this costs nothing.
//
// # Not a general "forward what I cannot parse" path
//
// That shape is a bypass with extra steps: the relay hides and allows
// whatever it fails to parse. Four constraints keep this narrow, and on three
// of them the codec refuses instead of forwarding.
//
//   - It opens only during the login phase, and only on bytes that form a
//     well-formed TLS record. Ciphertext before any PRELOGIN means the codec
//     can read nothing on the connection, so it refuses.
//   - It closes at the first byte that is not one, and LATCHES closed. A
//     connection gets one encrypted region; ciphertext after it would reopen
//     an uninspectable channel mid-session, so the codec refuses.
//   - It is bounded. Past maxEncryptedLogin the region is no longer a login.
//   - The codec refuses past that bound too. A session that never returns to
//     plaintext is ENCRYPT_ON, where nobody can inspect anything for the life
//     of the connection. Today an operator watches the lane break; forwarding
//     it forever would hide the bypass from them, which is worse.
//
// Statements decoded after a pass-through carry metaEncryptedLogin. An
// operator reading the trail then sees that this session's login crossed
// unobserved.

// metaEncryptedLogin is the Statement metadata key marking a session whose
// login crossed the relay as ciphertext.
const metaEncryptedLogin = "mssql.login_encrypted"

const (
	// tlsHeaderLen is the size of a TLS record header: content type (1),
	// version (2), length (2).
	tlsHeaderLen = 5

	// tlsRecordMax bounds a record's declared payload. RFC 8446 caps
	// plaintext at 2^14 and allows an expansion allowance on top, so
	// anything past 2^14+2048 is not a record header this codec should
	// follow.
	tlsRecordMax = 1<<14 + 2048

	// maxEncryptedLogin bounds the encrypted region.
	//
	// A login exchange is small: LOGIN7 plus, under integrated auth, the
	// SSPI continuations carrying a service ticket, which a large PAC can
	// push to a few tens of KiB. 32 KiB clears that with room and is
	// negligible against a session.
	//
	// serverCiphertext catches ENCRYPT_ON first, on the upstream's opening
	// reply, and catches it exactly. This bound covers the case that one
	// misses: a client streaming ciphertext while the server stays silent.
	// Under ENCRYPT_ON nobody could inspect those bytes whatever this codec
	// did, so the bound buys prompt detection. It protects nothing.
	maxEncryptedLogin = 32 << 10
)

// TLS record content types. Handshake and change-cipher-spec appear when a
// server sends its half of the handshake unwrapped; application data carries
// the login itself.
const (
	tlsChangeCipherSpec = 0x14
	tlsAlert            = 0x15
	tlsHandshake        = 0x16
	tlsApplicationData  = 0x17
)

// isTLSRecord reports whether b begins with a TLS record header.
//
// Callers must pass at least headerLen bytes. That is deliberate: a TDS
// header needs 8 and a record header needs 5, so waiting for the longer of
// the two means the two framings are never told apart on a short read.
func isTLSRecord(b []byte) bool {
	if len(b) < tlsHeaderLen {
		return false
	}
	switch b[0] {
	case tlsChangeCipherSpec, tlsAlert, tlsHandshake, tlsApplicationData:
	default:
		return false
	}
	// Record-layer version: 3.x, and TLS 1.3 still writes legacy 0x0303
	// here. Anything else is not a record this codec follows.
	if b[1] != 0x03 || b[2] > 0x04 {
		return false
	}
	n := int(binary.BigEndian.Uint16(b[3:5]))
	return n > 0 && n <= tlsRecordMax
}

// consumeEncrypted walks whole TLS records at the front of b.
//
// It returns how many bytes it consumed and whether the codec is still inside
// the encrypted region. The caller resumes TDS parsing at b[n:] only when
// inside is false.
//
// Callers enter this with c.inTLS already true, so the first iteration is
// known to be looking at a record.
func (c *Codec) consumeEncrypted(b []byte) (n int, inside bool, err error) {
	for {
		rest := b[n:]

		// Wait for enough bytes to tell a record header from a packet
		// header. headerLen is the longer of the two, so this never decides
		// on a short read.
		if len(rest) < headerLen {
			return n, true, nil
		}

		if !isTLSRecord(rest) {
			// Plaintext resumed, exactly here. Latch so the region cannot
			// reopen later in the connection.
			c.inTLS = false
			c.tlsResynced = true
			c.loginEncrypted = true
			return n, false, nil
		}

		recLen := tlsHeaderLen + int(binary.BigEndian.Uint16(rest[3:5]))
		if len(rest) < recLen {
			return n, true, nil // record not fully buffered yet
		}
		n += recLen

		c.tlsBytes += recLen
		if c.tlsBytes > maxEncryptedLogin {
			return n, true, fmt.Errorf(
				"%w: %d bytes of TLS records have crossed this connection without "+
					"plaintext TDS resuming, so the session is encrypted end to end "+
					"(PRELOGIN negotiated ENCRYPT_ON or ENCRYPT_REQ, not the "+
					"ENCRYPT_OFF login-only case); no statement on it can be "+
					"classified, masked or audited. Terminate the client's TLS in "+
					"front of the relay, or configure the lane for TDS 8.0",
				hoopinspect.ErrStreamUnsafe, c.tlsBytes)
		}
	}
}

// serverCiphertext refuses a raw TLS record arriving from the upstream.
//
// A server under ENCRYPT_OFF emits none, which is what makes this an exact
// signal rather than a threshold. Its handshake stays inside 0x12 packets
// (MS-TDS 3.3.5.2) and it sends the login response in the clear, verified on
// the wire against SQL Server 2019: PRELOGIN reply, 0x12-wrapped handshake,
// then plaintext. So a raw record here means the negotiation produced
// ENCRYPT_ON or ENCRYPT_REQ and every byte of the session is opaque.
//
// Catching it on the server's first post-handshake reply matters more than it
// looks. The client-side byte bound cannot fire on this case at all: a client
// waiting for a login response sends nothing further, so the connection sits
// at a few KiB and stalls until the driver's own login timeout, which reports
// "Login timeout expired" and leaves the operator no reason. Refusing here
// turns that into a denial with a cause, in milliseconds.
//
// This cannot fire on a TDS 8.0 lane. Whatever terminates the client's TLS
// hands this codec plaintext TDS in both directions, so no raw record reaches
// it. One arriving means nothing terminated that TLS.
func (c *Codec) serverCiphertext(b []byte) error {
	if len(b) < headerLen || !isTLSRecord(b) {
		return nil
	}
	return fmt.Errorf(
		"%w: the upstream answered with TLS records instead of TDS packets, so "+
			"PRELOGIN negotiated ENCRYPT_ON or ENCRYPT_REQ and the whole session "+
			"is encrypted, not just the login (the ENCRYPT_OFF case this relay "+
			"reads through); no statement on it can be classified, masked or "+
			"audited. Terminate the client's TLS in front of the relay, or move "+
			"the lane to TDS 8.0",
		hoopinspect.ErrStreamUnsafe)
}

// encryptedRegion is the hook both directions call at a packet boundary.
//
// allowEnter scopes the one chance a connection gets to open the region to
// its login phase: the client direction gates on having seen a PRELOGIN, the
// server direction on the routing scan still being live.
//
// Ciphertext outside that window is refused rather than followed. Both
// refusals name a real deployment fault that today produces a desynchronized
// packet walk and no usable diagnostic.
func (c *Codec) encryptedRegion(b []byte, allowEnter bool) (n int, inside bool, err error) {
	if !c.inTLS {
		if len(b) < headerLen || !isTLSRecord(b) {
			return 0, false, nil
		}
		switch {
		case c.tlsResynced:
			// The login region already closed. A second one would be an
			// invisible channel reopened after inspection began, which is
			// what latching exists to deny.
			return 0, false, fmt.Errorf(
				"%w: the stream returned to TLS records after the login and after "+
					"plaintext TDS had resumed; statements inside that region "+
					"cannot be classified, masked or audited, and following it "+
					"would reopen an uninspectable channel mid-session",
				hoopinspect.ErrStreamUnsafe)
		case !allowEnter:
			// TLS before any PRELOGIN: the session is encrypted from its
			// first byte. That is TDS 8.0 reaching the relay with nothing
			// in front terminating it.
			return 0, false, fmt.Errorf(
				"%w: the connection is TLS from its first byte, so no TDS packet "+
					"on it is readable; this lane expects its client TLS "+
					"terminated upstream (Envoy's DownstreamTlsContext for TDS "+
					"8.0) and is receiving it unterminated",
				hoopinspect.ErrStreamUnsafe)
		}
		c.inTLS = true
	}
	return c.consumeEncrypted(b)
}
