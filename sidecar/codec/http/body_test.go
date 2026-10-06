package http_test

import (
	"testing"

	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
)

// CaptureRequestBody keeps what the client sends and drops what the server
// answers. CaptureBody keeps both.
func TestCaptureRequestBodyKeepsOnlyTheRequestSide(t *testing.T) {
	const resp = "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\nsent"
	for _, tc := range []struct {
		name     string
		opts     codechttp.Options
		wantResp string
	}{
		{"request only", codechttp.Options{CaptureRequestBody: true}, ""},
		{"both", codechttp.Options{CaptureBody: true, CaptureRequestBody: true}, "sent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := codechttp.New(tc.opts)
			req := decodeRequest(t, c, "POST /x HTTP/1.1\r\nHost: h\r\nContent-Length: 2\r\n\r\n{}")
			if req.HTTP.Body != "{}" {
				t.Errorf("request body %q, want {}", req.HTTP.Body)
			}
			stmts, _, err := c.Decode(inspect.FromServer, []byte(resp))
			if err != nil || len(stmts) != 1 {
				t.Fatalf("Decode: %d statements, %v", len(stmts), err)
			}
			if got := stmts[0].HTTP.Body; got != tc.wantResp {
				t.Errorf("response body %q, want %q", got, tc.wantResp)
			}
		})
	}
}

// A holding lane leaves server WebSocket messages compressed, so a corrupt
// one passes instead of failing the server stream (EVL-375).
func TestCaptureRequestBodyDoesNotInflateServerMessages(t *testing.T) {
	c := codechttp.New(codechttp.Options{CaptureRequestBody: true})
	decodeRequest(t, c, "GET /ws HTTP/1.1\r\nHost: h\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	accept := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\nSec-WebSocket-Extensions: permessage-deflate\r\n\r\n"
	if _, _, err := c.Decode(inspect.FromServer, []byte(accept)); err != nil {
		t.Fatalf("101: %v", err)
	}
	// FIN, RSV1 (compressed), text; the payload is not valid DEFLATE.
	stmts, _, err := c.Decode(inspect.FromServer, []byte{0xc1, 3, 0xff, 0xff, 0xff})
	if err != nil || len(stmts) != 1 || stmts[0].HTTP.Body != "" {
		t.Fatalf("Decode: %+v, %v; want one message without a body", stmts, err)
	}
}
