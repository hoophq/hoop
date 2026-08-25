package conformance

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"io"
	"log/slog"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/hoophq/hoop/hoopinspect"
	"github.com/hoophq/hoop/hoopinspect/proxy"
	_ "github.com/hoophq/libhoop/v2/codec/postgres"
)

// pgwire constants, spelled out rather than imported: proxy keeps them
// unexported, and a conformance test that borrowed the implementation's own
// copy would agree with a wrong value.
const (
	pgTagAuth  byte   = 'R'
	pgAuthSASL uint32 = 10
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

// testTLSConfig mints a throwaway certificate for a fake upstream.
func testTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "upstream"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
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

// dribblingPGServer is a Postgres upstream that negotiates TLS and then
// sends its SASL offer one byte per write.
//
// The dribble is the whole point. A server sending the offer in one write
// hides the bug: stripChannelBinding sees a whole frame and rewrites it. Real
// servers split it only when a TLS record boundary happens to land inside
// those 43 bytes, which is why the failure shows up in one deployment and
// never in a test suite.
func dribblingPGServer(t *testing.T, offer []byte) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	cfg := testTLSConfig(t)

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
		if _, err := conn.Write([]byte{'S'}); err != nil {
			return
		}
		tc := tls.Server(conn, cfg)
		if err := tc.Handshake(); err != nil {
			return
		}
		defer tc.Close()
		for _, b := range offer {
			if _, err := tc.Write([]byte{b}); err != nil {
				return
			}
		}
		// Hold the connection open: closing here would race the relay's
		// forward and the client could see EOF before the offer arrives.
		time.Sleep(2 * time.Second)
	}()
	return ln.Addr().String()
}

// End to end through a real pump: the client must receive an offer it can
// act on, even though every byte arrived in its own Read.
//
// Testing saslReassembler alone leaves the wiring unguarded. Deleting the
// call from pump would restore the original bug with every unit test green,
// which is exactly how this shipped in the first place.
func TestPumpStripsAnOfferSplitAcrossReads(t *testing.T) {
	offer := pgSASLOffer("SCRAM-SHA-256-PLUS", "SCRAM-SHA-256")
	addr := dribblingPGServer(t, offer)

	srv, err := proxy.NewServer(proxy.Config{
		Network:     "tcp",
		Listen:      "127.0.0.1:0",
		Upstream:    addr,
		Protocol:    hoopinspect.Postgres,
		UpstreamTLS: &tls.Config{InsecureSkipVerify: true},
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	t.Cleanup(func() { srv.Close() })

	deadline := time.Now().Add(3 * time.Second)
	for srv.Addr() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if srv.Addr() == nil {
		t.Fatal("server did not bind")
	}

	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}

	// pgwire has no server greeting: the backend sends nothing until it has
	// read a startup packet, and the relay likewise waits for one before it
	// will pump anything. Sending it here is what a real client does first.
	startup := make([]byte, 8)
	binary.BigEndian.PutUint32(startup[0:4], 8)
	binary.BigEndian.PutUint32(startup[4:8], 3<<16) // protocol 3.0
	if _, err := c.Write(startup); err != nil {
		t.Fatal(err)
	}

	// Read the header, then exactly what it declares: a client that cannot
	// do this is desynchronized, which is the corruption the rewrite must
	// not cause.
	head := make([]byte, 5)
	if _, err := io.ReadFull(c, head); err != nil {
		t.Fatalf("reading the message header: %v", err)
	}
	if head[0] != pgTagAuth {
		t.Fatalf("first byte = %q, want %q", head[0], pgTagAuth)
	}
	body := make([]byte, int(binary.BigEndian.Uint32(head[1:5]))-4)
	if _, err := io.ReadFull(c, body); err != nil {
		t.Fatalf("the declared length overran the message: %v", err)
	}

	got := mechanismsOf(t, append(head, body...))
	if len(got) != 1 || got[0] != "SCRAM-SHA-256" {
		t.Errorf("client was offered %v; libpq refuses -PLUS on a connection "+
			"it knows is unencrypted and fails the login", got)
	}
}
