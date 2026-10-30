package proxy_test

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	httpcodec "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// An http lane used to cut every response at 8 MiB: the codec consumed
// nothing of a body until it was whole, so the inspector's reassembly buffer
// overflowed and the lane answered 403 "message exceeds max buffer"
// (ENG-946). A body now streams through the lane with no size limit; only
// what masking must hold as one unit (one JSON document, a compressed
// body) is bounded, at 16 MiB.

const largeBody = 20 << 20

// tsvRows is a tab-separated body of about n bytes, one email per line.
func tsvRows(n int) []byte {
	var b bytes.Buffer
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "%d\tada@example.com\t%s\n", i, strings.Repeat("x", 48))
	}
	return b.Bytes()
}

func chunked(body []byte) []byte {
	var b bytes.Buffer
	for i := 0; i < len(body); i += 1 << 16 {
		part := body[i:min(i+1<<16, len(body))]
		fmt.Fprintf(&b, "%x\r\n%s\r\n", len(part), part)
	}
	b.WriteString("0\r\n\r\n")
	return b.Bytes()
}

func tsvResponse(framing string, body []byte) []byte {
	head := "HTTP/1.1 200 OK\r\nContent-Type: text/tab-separated-values\r\n"
	switch framing {
	case "chunked":
		return append([]byte(head+"Transfer-Encoding: chunked\r\n\r\n"), chunked(body)...)
	case "content-length":
		return append([]byte(head+fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))), body...)
	case "close-delimited":
		return append([]byte(head+"Connection: close\r\n\r\n"), body...)
	}
	panic("unknown framing " + framing)
}

// parseResponse decodes what the client read, whatever framing the relay
// chose for it.
func parseResponse(t *testing.T, raw []byte) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(raw)), nil)
	if err != nil {
		t.Fatalf("read response: %v (%.200q)", err, raw)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, body
}

// Without a rule the lane forwards a 20 MiB response as it arrives, byte
// for byte, under every HTTP/1.1 body framing.
func TestLargeHTTPResponseReachesTheClientWholeWithoutRules(t *testing.T) {
	body := tsvRows(largeBody)
	for _, framing := range []string{"chunked", "content-length", "close-delimited"} {
		t.Run(framing, func(t *testing.T) {
			resp := tsvResponse(framing, body)
			srv := startServer(t, proxy.Config{
				Upstream:   oneShotUpstream(t, resp),
				Protocol:   inspect.HTTP,
				DenyWriter: proxy.ProtocolDenyWriter{},
			})
			got := rawExchange(t, srv.Addr().String(), "GET /rows HTTP/1.1\r\nHost: h\r\n\r\n")
			if !bytes.Equal(got, resp) {
				t.Fatalf("client read %d bytes, upstream sent %d; head %.120q", len(got), len(resp), got)
			}
		})
	}
}

// With a mask rule a 20 MiB text response reaches the client whole with
// every line masked, under every body framing; a binary body passes byte
// for byte.
func TestLargeHTTPResponseReachesTheClientWholeMasked(t *testing.T) {
	tsv := tsvRows(largeBody)
	masked := bytes.ReplaceAll(tsv, []byte("ada@example.com"), []byte("[REDACTED]"))
	for _, framing := range []string{"chunked", "content-length", "close-delimited"} {
		t.Run("tsv "+framing, func(t *testing.T) {
			srv := startServer(t, proxy.Config{
				Upstream:   oneShotUpstream(t, tsvResponse(framing, tsv)),
				Protocol:   inspect.HTTP,
				Masker:     emailMasker{},
				DenyWriter: proxy.ProtocolDenyWriter{},
			})
			got := rawExchange(t, srv.Addr().String(), "GET /rows HTTP/1.1\r\nHost: h\r\n\r\n")
			resp, body := parseResponse(t, got)
			if resp.StatusCode != 200 {
				t.Fatalf("status %s: %.200q", resp.Status, got)
			}
			if !bytes.Equal(body, masked) {
				t.Fatalf("body: %d bytes, want %d masked; unmasked lines=%d, masked lines=%d",
					len(body), len(masked), bytes.Count(body, []byte("ada@example.com")), bytes.Count(body, []byte("[REDACTED]")))
			}
		})
	}
	t.Run("binary", func(t *testing.T) {
		bin := make([]byte, largeBody)
		rand.NewChaCha8([32]byte{9, 4, 6}).Read(bin)
		resp := append([]byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nContent-Length: %d\r\n\r\n", len(bin))), bin...)
		srv := startServer(t, proxy.Config{
			Upstream:   oneShotUpstream(t, resp),
			Protocol:   inspect.HTTP,
			Masker:     emailMasker{},
			DenyWriter: proxy.ProtocolDenyWriter{},
		})
		got := rawExchange(t, srv.Addr().String(), "GET /data.parquet HTTP/1.1\r\nHost: h\r\n\r\n")
		_, body := parseResponse(t, got)
		if !bytes.Equal(body, bin) {
			t.Fatalf("binary body changed: got %d bytes, want %d", len(body), len(bin))
		}
	})
}

// The response statement, written when the body ends, keeps the body up to
// max_body_bytes under capture_body. A close-delimited body ends with the
// upstream connection, so its statement is the last thing the lane records.
func TestLargeHTTPResponseAuditKeepsTheBodyUpToMaxBodyBytes(t *testing.T) {
	body := tsvRows(4096)
	const limit = 100
	for _, framing := range []string{"chunked", "content-length", "close-delimited"} {
		t.Run(framing, func(t *testing.T) {
			sink := audit.NewMemorySink(64)
			srv := startServer(t, proxy.Config{
				Upstream:   oneShotUpstream(t, tsvResponse(framing, body)),
				Protocol:   inspect.HTTP,
				Audit:      sink,
				DenyWriter: proxy.ProtocolDenyWriter{},
				CodecFactory: func() inspect.Codec {
					return httpcodec.New(httpcodec.Options{CaptureBody: true, MaxBodyBytes: limit})
				},
			})
			got := rawExchange(t, srv.Addr().String(), "GET /rows HTTP/1.1\r\nHost: h\r\n\r\n")
			if _, b := parseResponse(t, got); !bytes.Equal(b, body) {
				t.Fatalf("body changed: %d bytes, want %d", len(b), len(body))
			}
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) && !hasKind(sink.Events(), audit.KindSessionEnd) {
				time.Sleep(10 * time.Millisecond)
			}
			var responses []audit.Event
			for _, ev := range sink.Events() {
				if ev.Kind == audit.KindStatement && ev.Direction == inspect.FromServer {
					responses = append(responses, ev)
				}
			}
			if len(responses) != 1 {
				t.Fatalf("got %d response statements, want 1: %+v", len(responses), sink.Events())
			}
			ev := responses[0]
			if ev.HTTP == nil || ev.HTTP.StatusCode != 200 {
				t.Fatalf("response statement = %+v, want a 200", ev)
			}
			if ev.HTTP.Body != string(body[:limit]) || !ev.HTTP.BodyTruncated {
				t.Fatalf("Body = %d bytes truncated=%v, want the first %d bytes and truncated", len(ev.HTTP.Body), ev.HTTP.BodyTruncated, limit)
			}
		})
	}
}
