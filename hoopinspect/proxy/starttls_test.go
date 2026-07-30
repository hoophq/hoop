package proxy

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoopinspect"
)

// pgSASLOffer builds an AuthenticationSASL message offering the named
// mechanisms, the way a server announces them.
func pgSASLOffer(mechs ...string) []byte {
	var body bytes.Buffer
	for _, m := range mechs {
		body.WriteString(m)
		body.WriteByte(0)
	}
	body.WriteByte(0) // the list terminator

	out := make([]byte, 9, 9+body.Len())
	out[0] = pgTagAuth
	binary.BigEndian.PutUint32(out[1:5], uint32(8+body.Len()))
	binary.BigEndian.PutUint32(out[5:9], pgAuthSASL)
	return append(out, body.Bytes()...)
}

// mechanismsOf reads the mechanism list back out of an AuthenticationSASL
// message, the way libpq would.
func mechanismsOf(t *testing.T, msg []byte) []string {
	t.Helper()
	if len(msg) < 9 {
		t.Fatalf("message too short: %q", msg)
	}
	length := int(binary.BigEndian.Uint32(msg[1:5]))
	if got := len(msg); got != length+1 {
		t.Fatalf("declared length %d does not describe %d bytes: a client would "+
			"read the wrong number and desynchronize", length, got)
	}
	var out []string
	for _, m := range bytes.Split(msg[9:length+1], []byte{0}) {
		if len(m) > 0 {
			out = append(out, string(m))
		}
	}
	return out
}

// libpq refuses a "-PLUS" mechanism on a connection it knows is unencrypted,
// so relaying the server's offer fails the connection before the user runs
// anything. The relay terminates upstream TLS, so the client cannot bind to
// that channel and the mechanism could never have worked.
func TestStripChannelBindingRemovesOnlyThePlusMechanism(t *testing.T) {
	in := pgSASLOffer("SCRAM-SHA-256-PLUS", "SCRAM-SHA-256")

	out, changed := stripChannelBinding(in)
	if !changed {
		t.Fatal("the -PLUS mechanism was left in the offer")
	}

	got := mechanismsOf(t, out)
	if len(got) != 1 || got[0] != "SCRAM-SHA-256" {
		t.Errorf("mechanisms = %v, want [SCRAM-SHA-256]", got)
	}
}

// Order varies by server version, and the plain mechanism must survive from
// either side of the list.
func TestStripChannelBindingHandlesEitherOrder(t *testing.T) {
	for _, mechs := range [][]string{
		{"SCRAM-SHA-256-PLUS", "SCRAM-SHA-256"},
		{"SCRAM-SHA-256", "SCRAM-SHA-256-PLUS"},
	} {
		out, changed := stripChannelBinding(pgSASLOffer(mechs...))
		if !changed {
			t.Fatalf("%v: nothing stripped", mechs)
		}
		if got := mechanismsOf(t, out); len(got) != 1 || got[0] != "SCRAM-SHA-256" {
			t.Errorf("%v: mechanisms = %v, want [SCRAM-SHA-256]", mechs, got)
		}
	}
}

// Anything that is not an AuthenticationSASL offering the mechanism must come
// back byte-identical. This runs on every server-direction chunk, so a false
// positive corrupts ordinary traffic.
func TestStripChannelBindingLeavesEverythingElseAlone(t *testing.T) {
	rowData := []byte("D\x00\x00\x00\x19\x00\x01\x00\x00\x00\x0fada@example.com")

	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"a DataRow", rowData},
		{"an offer without -PLUS", pgSASLOffer("SCRAM-SHA-256")},
		{"an empty buffer", nil},
		{"a truncated header", []byte{pgTagAuth, 0, 0}},
		{
			// AuthenticationOk (selector 0), not a SASL offer.
			"a different auth message",
			[]byte{pgTagAuth, 0, 0, 0, 8, 0, 0, 0, 0},
		},
		{
			// The tag and length say more is coming; rewriting a partial
			// message would corrupt it.
			"a SASL offer split across reads",
			pgSASLOffer("SCRAM-SHA-256-PLUS", "SCRAM-SHA-256")[:12],
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, changed := stripChannelBinding(tc.in)
			if changed {
				t.Error("reported a change on a message it must not touch")
			}
			if !bytes.Equal(out, tc.in) {
				t.Errorf("payload modified:\n got %q\nwant %q", out, tc.in)
			}
		})
	}
}

// A server may pipeline the next message into the same read. Bytes after the
// rewritten message must survive at their new offset.
func TestStripChannelBindingPreservesTrailingMessages(t *testing.T) {
	trailer := []byte("Z\x00\x00\x00\x05I") // ReadyForQuery
	in := append(pgSASLOffer("SCRAM-SHA-256-PLUS", "SCRAM-SHA-256"), trailer...)

	out, changed := stripChannelBinding(in)
	if !changed {
		t.Fatal("nothing stripped")
	}
	if !bytes.HasSuffix(out, trailer) {
		t.Errorf("the pipelined message was lost or corrupted: %q", out)
	}
	// The declared length must describe only its own message, not the trailer.
	length := int(binary.BigEndian.Uint32(out[1:5]))
	if got := mechanismsOf(t, out[:length+1]); len(got) != 1 || got[0] != "SCRAM-SHA-256" {
		t.Errorf("mechanisms = %v, want [SCRAM-SHA-256]", got)
	}
}

// --- SSLRequest negotiation ----------------------------------------------

// pgServer runs one fake Postgres that answers an SSLRequest with reply.
//
// A zero reply means "accept, then stay silent", for the deadline test. The
// connection is held open until cleanup: closing it would deliver EOF and the
// caller would fail fast for the wrong reason.
func pgServer(t *testing.T, reply byte) (addr string, got chan []byte) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	got = make(chan []byte, 1)
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		req := make([]byte, 8)
		if _, err := io.ReadFull(conn, req); err != nil {
			return
		}
		got <- req
		if reply != 0 {
			_, _ = conn.Write([]byte{reply})
			return
		}
		<-done // hold the connection open so the caller sees a timeout, not EOF
	}()
	return ln.Addr().String(), got
}

// The server must receive the exact 8-byte SSLRequest, not a ClientHello.
// Sending TLS bytes first is the bug this replaces: Postgres logs "received
// direct SSL connection request" and drops the connection.
func TestNegotiatePostgresTLSSendsTheSSLRequestPacket(t *testing.T) {
	addr, got := pgServer(t, 'S')

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := negotiatePostgresTLS(conn); err != nil {
		t.Fatalf("negotiation failed on an 'S' reply: %v", err)
	}

	req := <-got
	if len(req) != 8 {
		t.Fatalf("sent %d bytes, want 8", len(req))
	}
	if n := binary.BigEndian.Uint32(req[0:4]); n != 8 {
		t.Errorf("length field = %d, want 8", n)
	}
	if code := binary.BigEndian.Uint32(req[4:8]); code != pgSSLRequestCode {
		t.Errorf("code = %d, want %d (SSLRequest)", code, pgSSLRequestCode)
	}
}

// An operator who configured upstream_tls asked for an encrypted hop.
// Continuing in the clear because the server said no would send credentials
// unencrypted, which is the failure they were preventing.
func TestNegotiatePostgresTLSRefusesToDowngrade(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply byte
		want  string
	}{
		{"server declines with N", 'N', "refused TLS"},
		{"server errors", 'E', "rejected the SSLRequest"},
		{"server sends nonsense", 'X', "unexpected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr, _ := pgServer(t, tc.reply)
			conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()

			err = negotiatePostgresTLS(conn)
			if err == nil {
				t.Fatal("negotiation succeeded; the connection would carry credentials in the clear")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// A server that accepts the TCP connection then says nothing must not hang
// the caller. dialUpstream sets a deadline; this checks the read honors it.
func TestNegotiatePostgresTLSHonorsTheDeadline(t *testing.T) {
	addr, _ := pgServer(t, 0) // accepts, never replies

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- negotiatePostgresTLS(conn) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a silent server produced no error")
		}
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() {
			t.Errorf("error %q is not a timeout", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("negotiation ignored the deadline and hung")
	}
}

// startTLS must route Postgres through the SSLRequest exchange. Testing
// negotiatePostgresTLS alone leaves the wiring unguarded: deleting the call
// from startTLS would restore the original bug with every other test green.
//
// The handshake that follows is expected to fail here, because the fake
// server answers 'S' and then speaks no TLS. Reaching a TLS error at all
// proves the negotiation ran first.
func TestStartTLSNegotiatesBeforeHandshakingPostgres(t *testing.T) {
	addr, got := pgServer(t, 'S')

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}

	_, err = startTLS(conn, addr, hoopinspect.Postgres, &tls.Config{InsecureSkipVerify: true})
	if err == nil {
		t.Fatal("handshake succeeded against a server that speaks no TLS")
	}

	select {
	case req := <-got:
		if code := binary.BigEndian.Uint32(req[4:8]); code != pgSSLRequestCode {
			t.Errorf("first bytes on the wire were %v, not an SSLRequest", req)
		}
	default:
		t.Error("startTLS handshook without sending an SSLRequest; a real server " +
			"would log \"received direct SSL connection request\" and hang up")
	}
}

// HTTP has no in-band negotiation: TLS starts immediately. Sending an
// SSLRequest there would be garbage prepended to the handshake.
func TestStartTLSSkipsNegotiationForNonPostgres(t *testing.T) {
	addr, got := pgServer(t, 0)

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}

	// Fails: the fake server never completes a handshake. What matters is
	// what went out first.
	_, _ = startTLS(conn, addr, hoopinspect.HTTP, &tls.Config{InsecureSkipVerify: true})

	select {
	case req := <-got:
		if binary.BigEndian.Uint32(req[4:8]) == pgSSLRequestCode {
			t.Error("sent a Postgres SSLRequest on an HTTP lane")
		}
	default:
	}
}
