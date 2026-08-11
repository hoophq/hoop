package mssql

import (
	"encoding/binary"
	"fmt"

	"github.com/hoophq/hoopinspect"
)

// Login-only encryption, and how this codec survives it.
//
// # What the wire does
//
// PRELOGIN's ENCRYPTION option has four values, and only one of them means
// "no TLS":
//
//	0x00 ENCRYPT_OFF      encryption available, off -- BUT the login is still encrypted
//	0x01 ENCRYPT_ON       the whole session is encrypted
//	0x02 ENCRYPT_NOT_SUP  no encryption anywhere
//	0x03 ENCRYPT_REQ      the server requires encryption
//
// ENCRYPT_OFF is the trap. It reads as "encryption off" and means "encrypt
// the login only". A SQL Server with TLS administratively disabled still
// negotiates it: with no certificate installed the server mints a self-signed
// one at startup and encrypts credentials with that, so an operator who
// turned encryption off is still handed an encrypted login. The protocol
// behaving as specified, not a misconfiguration to chase.
//
// It is also the common case rather than an exotic one. go-mssqldb defaults
// to ENCRYPT_OFF (msdsn/conn_str.go:297, `var encryption Encryption =
// EncryptionOff`), so every Go client that does not set `encrypt=` takes this
// path. Only the modern Microsoft drivers avoid it, by defaulting to
// Mandatory and encrypting everything instead.
//
// # Exactly one PACKET is encrypted, not one message
//
// MS-TDS 3.2.5.3: "the first TDS packet of the Login message MUST be
// encrypted using TLS/SSL and encapsulated in a TLS/SSL message. All other
// TDS packets sent or received MUST be in plaintext."
//
// Packet, not message. A LOGIN7 larger than the negotiated packet size (4096
// by default) is split, and only the first fragment is inside TLS; the rest,
// credentials included, is plaintext on the wire. A decoder that assumed "TLS
// until the LOGIN7 message is covered" would run straight past the boundary,
// which is why this walks records and stops at the first byte that is not
// one.
//
// The driver's side of the switch is a single field assignment covering both
// directions (go-mssqldb tds.go:1262, `outbuf.afterFirst = func() {
// outbuf.transport = toconn }`, fired by buf.go:99 after the first packet
// flushes), so nothing can keep sending ciphertext after it.
//
// # Where the ciphertext sits
//
// The handshake rides inside 0x12 PRELOGIN packets, which the decoder already
// forwards untouched. What breaks it is what comes next: once the handshake
// completes the nesting inverts from TLS-inside-TDS to TDS-inside-TLS, and
// records go on the wire RAW with no TDS header. libhoop's own client does
// exactly this (agent/mssql/net.go, where tlsHandshakeConn stops wrapping
// once `upgraded` is set).
//
// A packet walk reads such a record as a header and misparses it. For an
// application-data record `17 03 03 04 00`, the type byte is 0x17 and
// BigEndian.Uint16(b[2:4]) is 0x0304 = 772, so the walk resumes 772 bytes
// into ciphertext and every offset after that is wrong. The connection does
// not fail; it goes quiet. Statements stop being decoded, the gate's
// honest-default forwards bytes it could not parse, and policy, masking and
// the audit trail all end with no signal beyond one error per read.
//
// The two directions are asymmetric. A client emits one raw region, the
// encrypted login packet. A server under ENCRYPT_OFF emits none: its whole
// handshake stays inside 0x12 (MS-TDS 3.3.5.2, or 0x04 from older endpoints)
// and its login response is already plaintext, so the routing-ENVCHANGE guard
// in decodeServer keeps working through an encrypted login. decodeServer runs
// the same walk anyway, as a safety net rather than a path with known
// traffic on it.
//
// # Why walking the region is exact rather than a guess
//
// TLS frames itself: a 5-byte header whose last two bytes are the payload
// length, big-endian. Following that framing consumes the encrypted region
// record by record and lands on precisely the byte where plaintext resumes.
// Nothing is decrypted and nothing is scanned for; the length is read from a
// field the sender wrote.
//
// The two framings cannot be confused. TDS packet types are 0x01-0x12 and TLS
// content types are 0x14-0x17, disjoint sets, and a record header is further
// required to carry a 0x03 version major and a plausible length. So the entry
// test is a structural check, not a heuristic.
//
// One record per packet is usual but not guaranteed: go-mssqldb forces
// DynamicRecordSizingDisabled, giving 16384-byte records, and a negotiated
// TDS packet size above that (TDS permits 32767) splits one packet across
// two. The walk handles any number of records, so this costs nothing.
//
// # Why this is not a general "forward what I cannot parse" path
//
// That would be a bypass with extra steps: anything unparseable becomes
// invisible and allowed. Four constraints keep this narrow, and three of the
// four fail closed rather than forwarding.
//
//   - It opens only during the login phase, and only on bytes that are a
//     well-formed TLS record. Ciphertext before any PRELOGIN means nothing on
//     the connection is readable, which is refused.
//   - It closes at the first byte that is not one, and LATCHES closed. A
//     connection gets one encrypted region; ciphertext after it would be an
//     uninspectable channel reopened mid-session, which is refused.
//   - It is bounded. Past maxEncryptedLogin the region is not a login.
//   - Running past that bound is refused too. A session that never returns to
//     plaintext is ENCRYPT_ON, where inspection is impossible for the life of
//     the connection. Allowing it would be the worse half of this bug rather
//     than the fix: today the lane is visibly broken, and silently forwarding
//     forever would make it invisibly bypassed.
//
// Statements decoded after a pass-through carry metaEncryptedLogin, so the
// audit trail states that the login was not observable instead of implying a
// fully inspected session.

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
	// The bound is a detector, not a shield. Under ENCRYPT_ON every byte is
	// ciphertext whatever this codec does, so the bytes inside the bound
	// were never inspectable; what the bound buys is finding out promptly
	// and saying so, instead of a connection that stays dark forever.
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
