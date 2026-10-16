package http_test

import (
	"fmt"
	"strings"
	"testing"

	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
)

var origin = &codechttp.Origin{Host: "api.example.com", Proto: "http"}

// originFilter runs one client stream through a fresh reverse-proxy codec's
// filter, split at every point and byte by byte, and requires want each time.
func originFilter(t *testing.T, in, want string) {
	t.Helper()
	run := func(chunks ...string) string {
		c := codechttp.New(codechttp.Options{Origin: origin})
		var out strings.Builder
		for _, chunk := range chunks {
			b, err := c.Filter(inspect.FromClient, []byte(chunk))
			if err != nil {
				t.Fatalf("Filter(%q): %v", chunk, err)
			}
			out.Write(b)
		}
		return out.String()
	}
	for i := 0; i <= len(in); i++ {
		if got := run(in[:i], in[i:]); got != want {
			t.Fatalf("split at %d:\n got %q\nwant %q", i, got, want)
		}
	}
	if got := run(bytewise(in)...); got != want {
		t.Fatalf("byte by byte:\n got %q\nwant %q", got, want)
	}
}

// A browser names the sidecar in Host. The origin must see its own name, or
// a virtual-hosted server or a CDN answers for a site it does not serve; the
// browser's name moves to X-Forwarded-Host.
func TestOriginRenamesHostAndForwardsTheClientsName(t *testing.T) {
	in := "GET /x HTTP/1.1\r\nHost: 127.0.0.1:19999\r\nAccept: */*\r\n\r\n"
	want := "GET /x HTTP/1.1\r\nHost: api.example.com\r\nAccept: */*\r\n" +
		"X-Forwarded-Host: 127.0.0.1:19999\r\nX-Forwarded-Proto: http\r\n" + via() + "\r\n"
	originFilter(t, in, want)
}

// The X-Forwarded fields are the sidecar's to write. A client's own copy,
// in any case and folded over two lines, is dropped: forwarded, it would
// reach the origin as if the sidecar had vouched for it.
func TestOriginReplacesClientForwardedFields(t *testing.T) {
	in := "GET /x HTTP/1.1\r\nx-forwarded-host: evil.example\r\nHost: h\r\n" +
		"X-FORWARDED-PROTO: https\r\n folded\r\nCookie: a=b\r\n\r\n"
	want := "GET /x HTTP/1.1\r\nHost: api.example.com\r\nCookie: a=b\r\n" +
		"X-Forwarded-Host: h\r\nX-Forwarded-Proto: http\r\n" + via() + "\r\n"
	originFilter(t, in, want)
}

// HTTP/1.0 may send no Host. The request still names the origin, and no
// X-Forwarded-Host claims a name the client never sent. Bare LF heads get
// lines in the same style.
func TestOriginAddsHostWhenTheClientSentNone(t *testing.T) {
	in := "GET / HTTP/1.0\nAccept: */*\n\n"
	want := "GET / HTTP/1.0\nHost: api.example.com\nAccept: */*\n" +
		"X-Forwarded-Proto: http\nVia: 1.0 " + codechttp.Pseudonym() + "\n\n"
	originFilter(t, in, want)
}

// Every request on a kept-alive connection is rewritten, and bodies pass
// byte-exact even when they hold something that looks like a head.
func TestOriginRewritesEveryRequestAndNoBody(t *testing.T) {
	const decoy = "GET /decoy HTTP/1.1\r\nHost: decoy\r\n\r\n"
	a := fmt.Sprintf("POST /a HTTP/1.1\r\nHost: h\r\nContent-Length: %d\r\n\r\n", len(decoy)) + decoy
	b := "GET /b HTTP/1.1\r\nHost: h\r\n\r\n"
	fwd := "X-Forwarded-Host: h\r\nX-Forwarded-Proto: http\r\n" + via() + "\r\n"
	want := fmt.Sprintf("POST /a HTTP/1.1\r\nHost: api.example.com\r\nContent-Length: %d\r\n", len(decoy)) +
		fwd + decoy + "GET /b HTTP/1.1\r\nHost: api.example.com\r\n" + fwd
	originFilter(t, a+b, want)
}

// conn drives one origin-lane codec the way the gate does: client bytes
// through the filter and then the decoder, server bytes through the decoder.
type conn struct {
	t *testing.T
	c *codechttp.Codec
}

func newConn(t *testing.T) *conn {
	return &conn{t: t, c: codechttp.New(codechttp.Options{Origin: origin})}
}

// send filters and decodes client bytes and returns what goes upstream, or
// the filter's refusal.
func (c *conn) send(data string) (string, error) {
	c.t.Helper()
	out, err := c.c.Filter(inspect.FromClient, []byte(data))
	if err != nil {
		return "", err
	}
	if _, _, err := c.c.Decode(inspect.FromClient, out); err != nil {
		c.t.Fatalf("decode request %q: %v", out, err)
	}
	return string(out), nil
}

func (c *conn) answer(data string) {
	c.t.Helper()
	if _, _, err := c.c.Decode(inspect.FromServer, []byte(data)); err != nil {
		c.t.Fatalf("decode response %q: %v", data, err)
	}
}

const (
	wsHead = "GET /ws HTTP/1.1\r\nHost: localhost:8080\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"
	ws101 = "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n"
	forged = "GET /next HTTP/1.1\r\nHost: evil.example\r\nX-Forwarded-Host: evil.example\r\n\r\n"
)

// A WebSocket handshake is an ordinary request until the switch: it names
// the origin, so the origin accepts it. Once the server answers 101 the
// frames pass untouched, even ones that look like a request head.
func TestOriginRewritesTheWebSocketHandshakeOnly(t *testing.T) {
	c := newConn(t)
	got, err := c.send(wsHead)
	want := "GET /ws HTTP/1.1\r\nHost: api.example.com\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n" +
		"X-Forwarded-Host: localhost:8080\r\nX-Forwarded-Proto: http\r\n" + via() + "\r\n"
	if err != nil || got != want {
		t.Fatalf("handshake:\n got %q, %v\nwant %q", got, err, want)
	}
	c.answer(ws101)
	frames := "\x81\x85\x01\x02\x03\x04" + "iggho" + forged
	out, err := c.c.Filter(inspect.FromClient, []byte(frames))
	if err != nil || string(out) != frames {
		t.Errorf("frames after the switch: %q, %v", out, err)
	}
}

// An Upgrade the server does not grant switches nothing, so the next
// request on the connection is still pointed at the origin, and a client
// cannot use a refused upgrade to send forged forwarding fields.
func TestOriginKeepsRewritingAfterARefusedUpgrade(t *testing.T) {
	c := newConn(t)
	if _, err := c.send("GET / HTTP/1.1\r\nHost: h\r\nUpgrade: bogus\r\nConnection: Upgrade\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	c.answer("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
	got, err := c.send(forged)
	want := "GET /next HTTP/1.1\r\nHost: api.example.com\r\n" +
		"X-Forwarded-Host: evil.example\r\nX-Forwarded-Proto: http\r\n" + via() + "\r\n"
	if err != nil || got != want {
		t.Errorf("after a refused upgrade:\n got %q, %v\nwant %q", got, err, want)
	}
}

// The answer that grants a switch is the one to the request that asked for
// it. A 200 to an earlier request is not a 2xx to the CONNECT behind it, and
// once the CONNECT itself is refused the next request is rewritten.
func TestOriginMatchesTheSwitchToItsRequest(t *testing.T) {
	c := newConn(t)
	if _, err := c.send("GET /a HTTP/1.1\r\nHost: h\r\n\r\nCONNECT db:5432 HTTP/1.1\r\nHost: db:5432\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	c.answer("HTTP/1.1 100 Continue\r\n\r\nHTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
	c.answer("HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
	got, err := c.send(forged)
	if err != nil || !strings.Contains(got, "Host: api.example.com\r\n") {
		t.Errorf("after a refused CONNECT: %q, %v", got, err)
	}

	early := newConn(t)
	if _, err := early.send("GET /a HTTP/1.1\r\nHost: h\r\n\r\nCONNECT db:5432 HTTP/1.1\r\nHost: db:5432\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	early.answer("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
	if got, err := early.send(forged); err == nil {
		t.Errorf("a 200 to the GET let bytes through before the CONNECT was answered: %q", got)
	}
}

// Bytes the client sends before the server answered its switch request are
// refused, whatever they look like: rewritten as HTTP they would corrupt a
// tunnel the server then opens, passed through they would skip the rewrite.
// So is a head too large to frame, a malformed chunked body, and garbage.
func TestOriginRefusesWhatItCannotFrame(t *testing.T) {
	for name, early := range map[string]string{
		"frames":           "\x81\x85\x01\x02\x03\x04iggho",
		"a pipelined head": forged,
	} {
		c := newConn(t)
		if _, err := c.send(wsHead); err != nil {
			t.Fatal(err)
		}
		if got, err := c.send(early); err == nil {
			t.Errorf("%s before the switch was answered went out as %q", name, got)
		}
	}
	// In the same read as the switch request, too.
	if got, err := newConn(t).send(wsHead + forged); err == nil {
		t.Errorf("a head pipelined behind the switch request went out as %q", got)
	}
	for name, in := range map[string]string{
		"oversized head": "GET / HTTP/1.1\r\nHost: h\r\nX-Pad: " + strings.Repeat("a", 1<<20+1),
		"bad chunk end":  "POST / HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nabcXY",
		"bad chunk size": "POST / HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\n\r\nzz\r\n",
		"garbage":        "not a request line\r\n\r\n",
	} {
		c := codechttp.New(codechttp.Options{Origin: origin})
		if out, err := c.Filter(inspect.FromClient, []byte(in)); err == nil {
			t.Errorf("%s: forwarded %d bytes instead of refusing", name, len(out))
		} else if _, err2 := c.Filter(inspect.FromClient, []byte(forged)); err2 == nil {
			t.Errorf("%s: the connection accepted a request after the refusal", name)
		}
	}
}

// A target in absolute form names a host, and a recipient obeys it ahead of
// Host. The request goes out in origin form, so the origin is the one
// configured; X-Forwarded-Host keeps the Host field the client sent.
func TestOriginRewritesAnAbsoluteTarget(t *testing.T) {
	in := "GET http://other.example/x/%2F?q=1 HTTP/1.1\r\nHost: 127.0.0.1:19999\r\n\r\n"
	want := "GET /x/%2F?q=1 HTTP/1.1\r\nHost: api.example.com\r\n" +
		"X-Forwarded-Host: 127.0.0.1:19999\r\nX-Forwarded-Proto: http\r\n" + via() + "\r\n"
	originFilter(t, in, want)
}

// Without an Origin the lane forwards Host as the client wrote it, and adds
// no X-Forwarded field: a host:port lane keeps its behaviour.
func TestNoOriginLeavesHostAlone(t *testing.T) {
	head := "GET /x HTTP/1.1\r\nHost: 127.0.0.1:19999\r\nX-Forwarded-Host: kept\r\n"
	if got := filterChunks(t, head+"\r\n"); got != head+via()+"\r\n" {
		t.Errorf("got %q", got)
	}
}
