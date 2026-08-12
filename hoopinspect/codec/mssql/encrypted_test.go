package mssql_test

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/hoophq/hoopinspect"
)

// PRELOGIN ENCRYPTION values.
const (
	encryptOff    = 0x00 // available, off -- the login is STILL encrypted
	encryptOn     = 0x01
	encryptNotSup = 0x02
)

// preloginBody builds a PRELOGIN payload carrying VERSION and ENCRYPTION.
//
// Layout: option entries of {token, uint16 BE offset, uint16 BE length}
// terminated by 0xFF, then the option data. Offsets are relative to the
// payload start.
//
// The body is separate from the framing because the two directions frame it
// differently: a client sends PRELOGIN as 0x12, and a server answers with
// 0x04.
func preloginBody(encryption byte) []byte {
	const tableLen = 5 + 5 + 1
	var body []byte

	body = append(body, 0x00) // VERSION
	body = binary.BigEndian.AppendUint16(body, tableLen)
	body = binary.BigEndian.AppendUint16(body, 6)

	body = append(body, 0x01) // ENCRYPTION
	body = binary.BigEndian.AppendUint16(body, tableLen+6)
	body = binary.BigEndian.AppendUint16(body, 1)

	body = append(body, 0xFF)
	body = append(body, 15, 0, 0, 0, 0, 0)
	body = append(body, encryption)
	return body
}

func preloginPacket(encryption byte) []byte {
	return tdsPacket(0x12, true, preloginBody(encryption))
}

// tlsRecord builds a TLS record: content type, version, uint16 BE length.
// The payload is zeroed; nothing here ever decrypts it.
func tlsRecord(contentType byte, payloadLen int) []byte {
	r := []byte{contentType, 0x03, 0x03}
	r = binary.BigEndian.AppendUint16(r, uint16(payloadLen))
	return append(r, make([]byte, payloadLen)...)
}

// encryptOffLogin is what a TDS 7.x client puts on the wire when PRELOGIN
// negotiates ENCRYPT_OFF: a PRELOGIN, a TLS handshake wrapped in 0x12
// packets, then the LOGIN7 as a RAW application-data record with no TDS
// header at all.
func encryptOffLogin() []byte {
	var s []byte
	s = append(s, preloginPacket(encryptOff)...)
	s = append(s, tdsPacket(0x12, true, tlsRecord(0x16, 512))...) // ClientHello
	s = append(s, tdsPacket(0x12, true, tlsRecord(0x14, 1))...)   // ChangeCipherSpec
	s = append(s, tlsRecord(0x17, 1024)...)                       // LOGIN7, raw
	return s
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// splitReads chops a stream the way a socket does: fixed-size reads that
// respect no protocol boundary. Packet-aligned chunks hide this bug, which is
// why it reads as intermittent in the field.
func splitReads(stream []byte, size int) [][]byte {
	var out [][]byte
	for len(stream) > 0 {
		n := min(size, len(stream))
		out = append(out, stream[:n])
		stream = stream[n:]
	}
	return out
}

func inspectAll(t *testing.T, insp *hoopinspect.Inspector, chunks [][]byte) ([]hoopinspect.Statement, error) {
	t.Helper()
	var all []hoopinspect.Statement
	for _, c := range chunks {
		stmts, err := insp.Inspect(hoopinspect.FromClient, c)
		all = append(all, stmts...)
		if err != nil {
			return all, err
		}
	}
	return all, nil
}

// A SQL Server with TLS administratively disabled still encrypts the login,
// because ENCRYPT_OFF means "encrypt the login only". Every statement after
// it is plaintext and must be inspected.
//
// The read sizes matter. Walking TLS records as if they were TDS headers
// desynchronizes the parse, and whether the damage is visible depends on
// where the socket happens to split the stream.
func TestStatementsResumeAfterAnEncryptedLogin(t *testing.T) {
	stream := concat(
		encryptOffLogin(),
		sqlBatch("SELECT name FROM customers"),
		sqlBatch("DELETE FROM customers WHERE id = 1"),
	)

	for _, readSize := range []int{64, 512, 4096, 65536} {
		t.Run(readSizeName(readSize), func(t *testing.T) {
			stmts, err := inspectAll(t, newInspector(t), splitReads(stream, readSize))
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			if len(stmts) != 2 {
				t.Fatalf("got %d statements, want 2", len(stmts))
			}
			if stmts[0].Operation != hoopinspect.OpSelect {
				t.Errorf("first statement = %v, want select", stmts[0].Operation)
			}
			// The one that matters: a DELETE that reaches the server
			// unclassified is a guardrail that did not run.
			if stmts[1].Operation != hoopinspect.OpDelete {
				t.Errorf("second statement = %v, want delete", stmts[1].Operation)
			}
		})
	}
}

func readSizeName(n int) string {
	switch n {
	case 64:
		return "read64"
	case 512:
		return "read512"
	case 4096:
		return "read4096"
	}
	return "whole-stream"
}

// The audit trail has to say the login was never observable. Without this an
// operator reading the session cannot tell it apart from one where the whole
// exchange was in the clear.
func TestStatementsAfterAnEncryptedLoginAreMarked(t *testing.T) {
	stmts, err := inspectAll(t, newInspector(t), [][]byte{
		concat(encryptOffLogin(), sqlBatch("SELECT 1")),
	})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want 1", len(stmts))
	}
	if stmts[0].Metadata["mssql.login_encrypted"] != "true" {
		t.Errorf("mssql.login_encrypted = %q, want \"true\"",
			stmts[0].Metadata["mssql.login_encrypted"])
	}
}

// A lane whose client TLS is terminated upstream must not gain the marker,
// or every TDS 8.0 session would claim a blind spot it does not have.
func TestPlaintextLoginCarriesNoMarker(t *testing.T) {
	stmts, err := inspectAll(t, newInspector(t), [][]byte{concat(
		preloginPacket(encryptNotSup),
		tdsPacket(0x10, true, make([]byte, 64)), // LOGIN7, in the clear
		sqlBatch("SELECT 1"),
	)})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want 1", len(stmts))
	}
	if _, marked := stmts[0].Metadata["mssql.login_encrypted"]; marked {
		t.Error("a plaintext login was marked as encrypted")
	}
}

// ENCRYPT_ON encrypts the whole session, so plaintext never resumes and no
// statement is ever inspectable. Forwarding it forever would turn a visibly
// broken lane into an invisibly bypassed one, so it fails closed.
func TestFullySessionEncryptionFailsClosed(t *testing.T) {
	stream := []byte(preloginPacket(encryptOn))
	// Well past maxEncryptedLogin, with no plaintext anywhere.
	for range 6 {
		stream = append(stream, tlsRecord(0x17, 8192)...)
	}

	_, err := inspectAll(t, newInspector(t), [][]byte{stream})
	if !errors.Is(err, hoopinspect.ErrStreamUnsafe) {
		t.Fatalf("a fully encrypted session returned %v, want ErrStreamUnsafe", err)
	}
	if !strings.Contains(err.Error(), "encrypted end to end") {
		t.Errorf("error does not name the cause: %v", err)
	}
}

// The pass-through opens once. Ciphertext appearing after plaintext resumed
// would be an uninspectable channel reopened mid-session, which is exactly
// what a client would do to smuggle a statement past the gate.
func TestCiphertextAfterTheLoginIsRefused(t *testing.T) {
	insp := newInspector(t)

	stmts, err := inspectAll(t, insp, [][]byte{
		concat(encryptOffLogin(), sqlBatch("SELECT 1")),
	})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("setup decoded %d statements, want 1", len(stmts))
	}

	// A second encrypted region, with a statement behind it.
	_, err = insp.Inspect(hoopinspect.FromClient,
		concat(tlsRecord(0x17, 512), sqlBatch("DELETE FROM customers")))
	if !errors.Is(err, hoopinspect.ErrStreamUnsafe) {
		t.Fatalf("a reopened encrypted region returned %v, want ErrStreamUnsafe", err)
	}
}

// TLS before any PRELOGIN means the connection is encrypted from its first
// byte: TDS 8.0 arriving with nothing in front terminating it. Nothing on it
// is readable, and the error has to say so rather than leave an operator
// reading a desynchronized packet walk.
func TestUnterminatedTLSFromTheFirstByteIsRefused(t *testing.T) {
	_, err := inspectAll(t, newInspector(t), [][]byte{tlsRecord(0x16, 512)})
	if !errors.Is(err, hoopinspect.ErrStreamUnsafe) {
		t.Fatalf("unterminated TLS returned %v, want ErrStreamUnsafe", err)
	}
	if !strings.Contains(err.Error(), "TLS from its first byte") {
		t.Errorf("error does not name the cause: %v", err)
	}
}

// The routing guard is the control that stops a driver being redirected off
// the relay. An encrypted client login must not cost it, and it does not: a
// server under ENCRYPT_OFF keeps its handshake inside 0x12 packets and sends
// the login response in the clear. Verified on the wire against SQL Server
// 2019, whose reply stream is PRELOGIN, 0x12-wrapped handshake, plaintext.
func TestRoutingRedirectStillRefusedAfterAnEncryptedLogin(t *testing.T) {
	insp := newInspector(t)
	stream := concat(
		tdsPacket(0x04, true, preloginBody(encryptOff)), // PRELOGIN reply
		tdsPacket(0x12, true, tlsRecord(0x16, 256)),     // handshake, TDS-wrapped
		loginResponse(routingEnvChange("secondary.corp.example", 1433)),
	)

	_, err := insp.Inspect(hoopinspect.FromServer, stream)
	if !errors.Is(err, hoopinspect.ErrStreamUnsafe) {
		t.Fatalf("routing redirect after an encrypted login returned %v, want ErrStreamUnsafe", err)
	}
	if !strings.Contains(err.Error(), "secondary.corp.example") {
		t.Errorf("error does not name the redirect target: %v", err)
	}
}

// A raw TLS record from the upstream means ENCRYPT_ON: the whole session is
// opaque, not just the login.
//
// Refusing on the server's reply is what makes this case detectable at all.
// A client waiting for a login response sends nothing more, so the
// client-side byte bound never fires and the connection stalls until the
// driver's login timeout, which tells the operator nothing.
func TestServerCiphertextIsRefusedImmediately(t *testing.T) {
	insp := newInspector(t)
	stream := concat(
		tdsPacket(0x04, true, preloginBody(encryptOn)),
		tdsPacket(0x12, true, tlsRecord(0x16, 256)),
		tlsRecord(0x17, 512), // the encrypted login response
	)

	_, err := insp.Inspect(hoopinspect.FromServer, stream)
	if !errors.Is(err, hoopinspect.ErrStreamUnsafe) {
		t.Fatalf("server ciphertext returned %v, want ErrStreamUnsafe", err)
	}
	if !strings.Contains(err.Error(), "whole session") {
		t.Errorf("error does not name the cause: %v", err)
	}
}

// MS-TDS 3.2.5.3 encrypts "the first TDS packet of the Login message", not
// the message. A LOGIN7 past the negotiated packet size (4096 by default) is
// split, and every fragment after the first is plaintext on the wire.
//
// This is the trap a decoder falls into by reasoning in messages: waiting for
// the whole LOGIN7 to be covered runs the walk past the boundary and back
// into the desynchronized parse the pass-through exists to prevent.
func TestOnlyTheFirstLoginPacketIsEncrypted(t *testing.T) {
	stream := concat(
		preloginPacket(encryptOff),
		tdsPacket(0x12, true, tlsRecord(0x16, 512)), // handshake
		tlsRecord(0x17, 4120),                       // LOGIN7 packet 1, encrypted
		tdsPacket(0x10, false, make([]byte, 4088)),  // packet 2, PLAINTEXT
		tdsPacket(0x10, true, make([]byte, 2000)),   // packet 3, PLAINTEXT, EOM
		sqlBatch("DELETE FROM customers WHERE id = 1"),
	)

	for _, readSize := range []int{512, 4096, 65536} {
		t.Run(readSizeName(readSize), func(t *testing.T) {
			stmts, err := inspectAll(t, newInspector(t), splitReads(stream, readSize))
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			if len(stmts) != 1 {
				t.Fatalf("got %d statements, want 1", len(stmts))
			}
			if stmts[0].Operation != hoopinspect.OpDelete {
				t.Errorf("statement = %v, want delete", stmts[0].Operation)
			}
		})
	}
}

// A TDS packet must never be mistaken for a TLS record. The type spaces are
// disjoint (TDS 0x01-0x12, TLS 0x14-0x17), which is what makes the entry test
// structural rather than a heuristic.
func TestNoTDSPacketLooksLikeATLSRecord(t *testing.T) {
	for _, typ := range []byte{0x01, 0x03, 0x04, 0x06, 0x07, 0x08, 0x0E, 0x0F, 0x10, 0x11, 0x12} {
		insp := newInspector(t)
		// Seed the login phase so the pass-through is allowed to open,
		// then hand it a real packet of every type.
		if _, err := insp.Inspect(hoopinspect.FromClient, preloginPacket(encryptOff)); err != nil {
			t.Fatalf("prelogin: %v", err)
		}
		body := sqlBatchBody("SELECT 1")
		if _, err := insp.Inspect(hoopinspect.FromClient, tdsPacket(typ, true, body)); err != nil {
			t.Errorf("packet type 0x%02X was taken for ciphertext: %v", typ, err)
		}
	}
}
