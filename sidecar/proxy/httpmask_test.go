package proxy_test

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// routeUpstream is an HTTP/1.1 origin that answers each request with the
// response its path names, and records the request lines it was sent.
type routeUpstream struct {
	ln     net.Listener
	mu     sync.Mutex
	got    []string
	routes map[string][]byte
}

func newRouteUpstream(t *testing.T, routes map[string][]byte) *routeUpstream {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("upstream listen: %v", err)
	}
	u := &routeUpstream{ln: ln, routes: routes}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				br := bufio.NewReader(c)
				for {
					req, err := http.ReadRequest(br)
					if err != nil {
						return
					}
					body, _ := io.ReadAll(req.Body)
					u.mu.Lock()
					u.got = append(u.got, req.Method+" "+req.URL.Path+" "+string(body))
					u.mu.Unlock()
					if _, err := c.Write(u.routes[req.URL.Path]); err != nil {
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return u
}

func (u *routeUpstream) addr() string { return u.ln.Addr().String() }

func (u *routeUpstream) received() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.got...)
}

func jsonResponse(body []byte, extraHeaders string) []byte {
	return append([]byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n%sContent-Length: %d\r\n\r\n",
		extraHeaders, len(body))), body...)
}

// roundTrip sends one request on a fresh connection and returns what came
// back before the relay closed it or the deadline passed.
func roundTrip(t *testing.T, addr, raw string) string {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	if _, err := c.Write([]byte(raw)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.Status + "\n" + string(body)
}

// emailRows is a JSON_ARRAY-shaped body of about n bytes, one email per row.
func emailRows(n int) []byte {
	var b bytes.Buffer
	b.WriteString(`{"result":{"data_array":[`)
	for i := 0; b.Len() < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `["%d","ada@example.com","%s"]`, i, strings.Repeat("x", 64))
	}
	b.WriteString(`]}}`)
	return b.Bytes()
}

// An uncompressed JSON response larger than the inspection buffer, on a lane
// with a masker, is refused and the relay keeps serving. It used to answer
// 403 and then crash the whole process: discarding the held body called a
// nil masker (ENG-817).
func TestOversizedJSONResponseIsRefusedAndTheRelayKeepsServing(t *testing.T) {
	up := newRouteUpstream(t, map[string][]byte{
		"/big":   jsonResponse(emailRows(9<<20), ""),
		"/small": jsonResponse([]byte(`{"e":"ada@example.com"}`), ""),
	})
	srv := startServer(t, proxy.Config{
		Upstream:   up.addr(),
		Protocol:   inspect.HTTP,
		Masker:     emailMasker{},
		DenyWriter: proxy.ProtocolDenyWriter{},
	})

	got := roundTrip(t, srv.Addr().String(), "GET /big HTTP/1.1\r\nHost: h\r\n\r\n")
	if !strings.HasPrefix(got, "403") || strings.Contains(got, "ada@example.com") {
		t.Fatalf("oversized response was not refused cleanly: %.200q", got)
	}
	got = roundTrip(t, srv.Addr().String(), "GET /small HTTP/1.1\r\nHost: h\r\n\r\n")
	if !strings.HasPrefix(got, "200") || !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("the lane stopped serving after the refusal: %q", got)
	}
}

// A gzip JSON response is small on the wire and can inflate past the mask
// limit (16 MiB). It is refused rather than forwarded unmasked.
func TestGzipJSONInflatingPastTheMaskLimitIsRefused(t *testing.T) {
	var zb bytes.Buffer
	zw := gzip.NewWriter(&zb)
	zw.Write(emailRows(17 << 20))
	zw.Close()
	up := newRouteUpstream(t, map[string][]byte{
		"/rows": jsonResponse(zb.Bytes(), "Content-Encoding: gzip\r\n"),
	})
	srv := startServer(t, proxy.Config{
		Upstream:   up.addr(),
		Protocol:   inspect.HTTP,
		Masker:     emailMasker{},
		DenyWriter: proxy.ProtocolDenyWriter{},
	})
	got := roundTrip(t, srv.Addr().String(), "GET /rows HTTP/1.1\r\nHost: h\r\nAccept-Encoding: gzip\r\n\r\n")
	if !strings.HasPrefix(got, "403") {
		t.Fatalf("a body too large to mask was not refused: %.120q", got)
	}
}

// rawExchange sends one request on a fresh connection and returns every
// byte that came back until the relay closed it.
func rawExchange(t *testing.T, addr, raw string) []byte {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	if _, err := c.Write([]byte(raw)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	got, _ := io.ReadAll(c)
	return got
}

// A chunked JSON stream goes out a value at a time. When a later value is
// too large to hold, the head and the first value already reached the
// client: the relay cuts the response there. It used to write a 403 status
// line inside the 200 response, which no client can parse. A lane without
// a mask rule forwards the stream as it arrives, so the same holds there.
func TestChunkedStreamRefusedMidResponseIsCut(t *testing.T) {
	for name, masker := range map[string]gate.Masker{"masked": emailMasker{}, "no mask rule": nil} {
		t.Run(name, func(t *testing.T) { chunkedStreamRefusedMidResponse(t, masker) })
	}
}

func chunkedStreamRefusedMidResponse(t *testing.T, masker gate.Masker) {
	var b strings.Builder
	b.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\n\r\n")
	first := `{"e":"ada@example.com"}`
	fmt.Fprintf(&b, "%x\r\n%s\r\n", len(first), first)
	big := emailRows(9 << 20)
	for i := 0; i < len(big); i += 1 << 16 {
		part := big[i:min(i+1<<16, len(big))]
		fmt.Fprintf(&b, "%x\r\n%s\r\n", len(part), part)
	}
	b.WriteString("0\r\n\r\n")
	up := newRouteUpstream(t, map[string][]byte{
		"/watch": []byte(b.String()),
		"/small": jsonResponse([]byte(`{"e":"ada@example.com"}`), ""),
	})
	cfg := proxy.Config{
		Upstream:   up.addr(),
		Protocol:   inspect.HTTP,
		DenyWriter: proxy.ProtocolDenyWriter{},
	}
	if masker != nil {
		cfg.Masker = masker
	}
	srv := startServer(t, cfg)

	got := rawExchange(t, srv.Addr().String(), "GET /watch HTTP/1.1\r\nHost: h\r\n\r\n")
	if n := bytes.Count(got, []byte("HTTP/1.1 ")); n != 1 || !bytes.HasPrefix(got, []byte("HTTP/1.1 200 OK")) {
		t.Fatalf("want the 200 alone, cut short; got %d status lines: %.300q", n, got)
	}
	if masker != nil && (bytes.Contains(got, []byte("ada@example.com")) || !bytes.Contains(got, []byte("[REDACTED]"))) {
		t.Fatalf("the first value was not masked, or the refused one leaked: %.300q", got)
	}
	if r := roundTrip(t, srv.Addr().String(), "GET /small HTTP/1.1\r\nHost: h\r\n\r\n"); !strings.HasPrefix(r, "200") {
		t.Fatalf("the lane stopped serving after the cut: %q", r)
	}
}

// oneShotUpstream answers one request with resp and closes the connection,
// so a body without Content-Length or chunking ends at the close.
func oneShotUpstream(t *testing.T, resp []byte) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("upstream listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		if _, err := http.ReadRequest(bufio.NewReader(c)); err != nil {
			return
		}
		_, _ = c.Write(resp)
	}()
	return ln.Addr().String()
}

// A close-delimited gzip body ends only when the upstream closes, with its
// head still held, so a body too large to mask gets the lane's 403. It used
// to vanish: the client read nothing at all.
func TestCloseDelimitedGzipTooLargeToMaskGets403(t *testing.T) {
	var zb bytes.Buffer
	zw := gzip.NewWriter(&zb)
	zw.Write(emailRows(17 << 20))
	zw.Close()
	resp := append([]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Encoding: gzip\r\nConnection: close\r\n\r\n"), zb.Bytes()...)
	srv := startServer(t, proxy.Config{
		Upstream:   oneShotUpstream(t, resp),
		Protocol:   inspect.HTTP,
		Masker:     emailMasker{},
		DenyWriter: proxy.ProtocolDenyWriter{},
	})
	got := rawExchange(t, srv.Addr().String(), "GET /rows HTTP/1.1\r\nHost: h\r\nAccept-Encoding: gzip\r\n\r\n")
	if !bytes.HasPrefix(got, []byte("HTTP/1.1 403 Forbidden")) || !bytes.Contains(got, []byte("MaxMessageBytes")) {
		t.Fatalf("want the lane's 403 naming the limit; got %.200q", got)
	}
}
