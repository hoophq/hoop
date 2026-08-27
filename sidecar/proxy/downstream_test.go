package proxy

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// readOnce reads a single chunk, treating a deadline expiry or a clean EOF as
// a result rather than a fault.
//
// Both are outcomes these tests assert on: a relay that answers nothing and a
// relay that closes are the two negative cases. Every other error means the
// harness broke, and reporting it as zero bytes read would surface as a
// puzzling assertion failure somewhere downstream instead of here.
func readOnce(c net.Conn, buf []byte) (int, error) {
	n, err := c.Read(buf)
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, os.ErrDeadlineExceeded) {
		return n, nil
	}
	return n, err
}

func pgPacket(code uint32) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint32(b[0:4], 8)
	binary.BigEndian.PutUint32(b[4:8], code)
	return b
}

func pgStartup() []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint32(b[0:4], 8)
	binary.BigEndian.PutUint32(b[4:8], 3<<16)
	return b
}

// tcpPair returns a connected client/server socket pair. Real sockets rather
// than net.Pipe: the pipe is synchronous and unbuffered, so it cannot model a
// client that writes a negotiation packet and its startup message in one go,
// which is exactly the case being tested.
func tcpPair(t *testing.T) (client, server net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()

	cli, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case srv := <-accepted:
		t.Cleanup(func() { cli.Close(); srv.Close() })
		return cli, srv
	case <-time.After(5 * time.Second):
		t.Fatal("no connection accepted")
		return nil, nil
	}
}

// negotiate runs negotiateDownstream against a client that writes `send`, and
// returns what the relay wrote back plus what the gate would then read.
func negotiate(t *testing.T, send []byte, tlsCfg *tls.Config) (reply []byte, gateSaw []byte) {
	t.Helper()
	cli, srv := tcpPair(t)

	done := make(chan struct{})
	var out []byte
	var gateErr error
	go func() {
		defer close(done)
		conn, _, err := negotiateDownstream(srv, inspect.Postgres, tlsCfg, 2*time.Second)
		if err != nil {
			return
		}
		buf := make([]byte, 512)
		if gateErr = conn.SetReadDeadline(time.Now().Add(2 * time.Second)); gateErr != nil {
			return
		}
		var n int
		if n, gateErr = readOnce(conn, buf); gateErr != nil {
			return
		}
		out = append([]byte(nil), buf[:n]...)
	}()

	if err := cli.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("client SetDeadline: %v", err)
	}
	if _, err := cli.Write(send); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 8)
	n, err := readOnce(cli, got)
	if err != nil {
		t.Fatalf("reading the relay's reply: %v", err)
	}

	<-done
	if gateErr != nil {
		t.Fatalf("gate side: %v", gateErr)
	}
	return got[:n], out
}

// The default for psql. A ticket in the cache makes libpq ask for a
// GSS-wrapped transport before anything else; accepting it would make every
// later byte unreadable, so the relay refuses.
func TestGSSEncryptionIsRefused(t *testing.T) {
	reply, _ := negotiate(t, pgPacket(pgGSSEncRequestCode), nil)
	if len(reply) == 0 || reply[0] != 'N' {
		t.Fatalf("reply = %q, want 'N'", reply)
	}
}

// 'E' would make pgjdbc close and reopen the TCP connection, doubling every
// login in the audit trail.
func TestGSSRefusalUsesNNotE(t *testing.T) {
	reply, _ := negotiate(t, pgPacket(pgGSSEncRequestCode), nil)
	if len(reply) > 0 && reply[0] == 'E' {
		t.Fatal("refused with 'E'; pgjdbc reconnects on 'E', doubling every session")
	}
}

// After the refusal the client falls back and sends its startup packet. That
// packet must reach the gate whole — the negotiation had to read 8 bytes to
// classify it, and those 8 bytes ARE the startup message.
func TestStartupSurvivesTheNegotiation(t *testing.T) {
	_, gateSaw := negotiate(t, append(pgPacket(pgGSSEncRequestCode), pgStartup()...), nil)
	if !bytes.Equal(gateSaw, pgStartup()) {
		t.Fatalf("gate saw %x, want the startup packet %x", gateSaw, pgStartup())
	}
}

// A client that never negotiates must be untouched: its first bytes are the
// startup packet and they all have to arrive.
func TestPlainStartupPassesThroughUnchanged(t *testing.T) {
	_, gateSaw := negotiate(t, pgStartup(), nil)
	if !bytes.Equal(gateSaw, pgStartup()) {
		t.Fatalf("gate saw %x, want %x", gateSaw, pgStartup())
	}
}

// With no certificate configured the relay cannot terminate TLS, so it says so
// rather than accepting and failing the handshake.
func TestSSLRefusedWhenNoCertificateConfigured(t *testing.T) {
	reply, _ := negotiate(t, pgPacket(pgSSLRequestCode), nil)
	if len(reply) == 0 || reply[0] != 'N' {
		t.Fatalf("reply = %q, want 'N'", reply)
	}
}

// The gate must receive a COMPLETE message, not the 8-byte prefix the
// negotiation had to read. A split here would forward the head of a statement
// upstream before policy could judge it.
func TestClassificationDoesNotFragmentTheStream(t *testing.T) {
	// A startup packet followed immediately by a Query, in one write.
	q := []byte{'Q'}
	q = binary.BigEndian.AppendUint32(q, uint32(4+len("SELECT 1")+1))
	q = append(q, "SELECT 1"...)
	q = append(q, 0)

	_, gateSaw := negotiate(t, append(pgStartup(), q...), nil)
	if len(gateSaw) != len(pgStartup())+len(q) {
		t.Fatalf("gate saw %d bytes, want %d: the stream was fragmented at the "+
			"classification boundary", len(gateSaw), len(pgStartup())+len(q))
	}
}

func selfSigned(t *testing.T) *tls.Config {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "relay"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"relay"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{{
		Certificate: [][]byte{der},
		PrivateKey:  key,
	}}}
}

// With a certificate the relay answers 'S' and completes a real handshake, so
// the client's leg is encrypted while the gate still reads plaintext.
func TestSSLRequestIsTerminatedWhenConfigured(t *testing.T) {
	cfg := selfSigned(t)
	cli, srv := tcpPair(t)

	type result struct {
		conn net.Conn
		err  error
	}
	res := make(chan result, 1)
	go func() {
		c, _, err := negotiateDownstream(srv, inspect.Postgres, cfg, 5*time.Second)
		res <- result{c, err}
	}()

	if err := cli.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("client SetDeadline: %v", err)
	}
	if _, err := cli.Write(pgPacket(pgSSLRequestCode)); err != nil {
		t.Fatal(err)
	}
	var ack [1]byte
	if _, err := io.ReadFull(cli, ack[:]); err != nil {
		t.Fatal(err)
	}
	if ack[0] != 'S' {
		t.Fatalf("reply = %q, want 'S'", ack[0])
	}

	// The client now speaks TLS. Completing the handshake proves the relay is
	// really terminating it and not merely claiming to.
	tc := tls.Client(cli, &tls.Config{InsecureSkipVerify: true})
	handshake := make(chan error, 1)
	go func() { handshake <- tc.Handshake() }()

	select {
	case err := <-handshake:
		if err != nil {
			t.Fatalf("client handshake failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handshake never completed")
	}

	// The startup packet the client sends inside TLS must reach the gate.
	if _, err := tc.Write(pgStartup()); err != nil {
		t.Fatal(err)
	}
	r := <-res
	if r.err != nil {
		t.Fatalf("relay side failed: %v", r.err)
	}
	buf := make([]byte, 8)
	if err := r.conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("relay SetReadDeadline: %v", err)
	}
	if _, err := io.ReadFull(r.conn, buf); err != nil {
		t.Fatalf("reading the startup packet through the terminated TLS: %v", err)
	}
	if !bytes.Equal(buf, pgStartup()) {
		t.Errorf("gate saw %x through TLS, want %x", buf, pgStartup())
	}
}

// Non-postgres lanes must not have their first bytes touched: TDS 8.0 is
// already inside TLS by this point and HTTP has no such exchange.
func TestOtherProtocolsAreNotIntercepted(t *testing.T) {
	cli, srv := net.Pipe()
	t.Cleanup(func() { cli.Close(); srv.Close() })

	got, _, err := negotiateDownstream(srv, inspect.MSSQL, nil, time.Second)
	if err != nil {
		t.Fatalf("mssql negotiation errored: %v", err)
	}
	if got != srv {
		t.Error("the connection was wrapped for a protocol that does not negotiate in-band")
	}
}

// pgStartupWithUser builds a v3 StartupMessage carrying the parameters libpq
// actually sends.
func pgStartupWithUser(user string) []byte {
	var params []byte
	for _, kv := range [][2]string{{"user", user}, {"database", "appdb"}, {"application_name", "psql"}} {
		params = append(params, kv[0]...)
		params = append(params, 0)
		params = append(params, kv[1]...)
		params = append(params, 0)
	}
	params = append(params, 0) // end of parameters

	out := make([]byte, 8, 8+len(params))
	binary.BigEndian.PutUint32(out[0:4], uint32(8+len(params)))
	binary.BigEndian.PutUint32(out[4:8], 3<<16)
	return append(out, params...)
}

// pgwire names its user in cleartext, so this lane can fill the actor column
// that every other one leaves anonymous.
func TestStartupUserIsRecovered(t *testing.T) {
	_, user, _ := negotiateTwo(t, pgStartupWithUser("alice@HOOP.TEST"))
	if user != "alice@HOOP.TEST" {
		t.Fatalf("user = %q, want alice@HOOP.TEST", user)
	}
}

// The client falls back to a plain startup after the refusal, and the name has
// to survive that. This is the real Kerberos sequence.
func TestStartupUserSurvivesAGSSRefusal(t *testing.T) {
	stream := append(pgPacket(pgGSSEncRequestCode), pgStartupWithUser("alice@HOOP.TEST")...)
	_, user, _ := negotiateTwo(t, stream)
	if user != "alice@HOOP.TEST" {
		t.Fatalf("user = %q after a GSS refusal, want alice@HOOP.TEST", user)
	}
}

// Reading the parameters must not consume them: the gate needs the packet.
func TestReadingTheUserLeavesTheStartupIntact(t *testing.T) {
	want := pgStartupWithUser("alice@HOOP.TEST")
	gateSaw, _, _ := negotiateTwo(t, want)
	if !bytes.Equal(gateSaw, want) {
		t.Fatalf("gate saw %d bytes, want the whole %d-byte startup packet",
			len(gateSaw), len(want))
	}
}

// A cancel request carries no session, so it must not invent a principal.
func TestCancelRequestYieldsNoPrincipal(t *testing.T) {
	_, user, _ := negotiateTwo(t, pgPacket(pgCancelRequestCode))
	if user != "" {
		t.Fatalf("user = %q, want empty for a cancel request", user)
	}
}

// negotiateTwo returns what the gate would read and the user recovered.
func negotiateTwo(t *testing.T, send []byte) (gateSaw []byte, user string, reply []byte) {
	t.Helper()
	cli, srv := tcpPair(t)

	done := make(chan struct{})
	var out []byte
	var got string
	var gateErr error
	go func() {
		defer close(done)
		conn, u, err := negotiateDownstream(srv, inspect.Postgres, nil, 2*time.Second)
		got = u
		if err != nil {
			return
		}
		buf := make([]byte, 1024)
		if gateErr = conn.SetReadDeadline(time.Now().Add(2 * time.Second)); gateErr != nil {
			return
		}
		var n int
		if n, gateErr = readOnce(conn, buf); gateErr != nil {
			return
		}
		out = append([]byte(nil), buf[:n]...)
	}()

	if err := cli.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("client SetDeadline: %v", err)
	}
	if _, err := cli.Write(send); err != nil {
		t.Fatal(err)
	}
	// Most of these lanes answer nothing at all, so the short deadline
	// expiring IS the expected result rather than a failure.
	ack := make([]byte, 1)
	if err := cli.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("client SetReadDeadline: %v", err)
	}
	n, err := readOnce(cli, ack)
	if err != nil {
		t.Fatalf("reading the relay's reply: %v", err)
	}

	<-done
	if gateErr != nil {
		t.Fatalf("gate side: %v", gateErr)
	}
	return out, got, ack[:n]
}
