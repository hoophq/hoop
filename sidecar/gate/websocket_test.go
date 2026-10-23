package gate_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	httpcodec "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/session"
)

// A WebSocket message sent in fragments used to leave fragment by fragment:
// the codec consumed each one as it arrived, and the gate forwards what the
// codec consumed. A rule that refused the whole message then could not
// recall its start.

const (
	wsUpgrade = "GET /chat HTTP/1.1\r\nHost: h\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"
	wsAccept = "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n"
)

// wsFrame is one masked frame, as a client sends it, under 64 KiB.
func wsFrame(fin bool, op byte, payload string) []byte {
	b0 := op
	if fin {
		b0 |= 0x80
	}
	out := []byte{b0}
	if n := len(payload); n < 126 {
		out = append(out, 0x80|byte(n))
	} else {
		out = append(out, 0x80|126, byte(n>>8), byte(n))
	}
	key := [4]byte{0x37, 0xfa, 0x21, 0x3d}
	out = append(out, key[:]...)
	for i := range len(payload) {
		out = append(out, payload[i]^key[i&3])
	}
	return out
}

// noDropTable refuses a client message whose whole body asks to drop a
// table. Neither fragment of "drop ta" + "ble users" does on its own.
type noDropTable struct{}

func (noDropTable) Evaluate(stmt inspect.Statement) policy.Verdict {
	if stmt.Operation == inspect.OpWSMessage && stmt.HTTP != nil && strings.Contains(stmt.HTTP.Body, "drop table") {
		return policy.Deny("no-drop-table", "dropping tables is refused on this lane")
	}
	return policy.Verdict{}
}

// upgraded is a gate past a WebSocket handshake, decoding client bodies so
// a policy can read them.
func upgraded(t *testing.T, maxBuffer int, opts httpcodec.Options) *gate.Gate {
	t.Helper()
	opts.CaptureRequestBody = true
	g, err := gate.New(session.New(inspect.HTTP, session.Identity{}), gate.Config{
		Protocol:     inspect.HTTP,
		Policy:       noDropTable{},
		MaxBuffer:    maxBuffer,
		CodecFactory: func() inspect.Codec { return httpcodec.New(opts) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	if d := g.Request(ctx, []byte(wsUpgrade)); !d.Allowed {
		t.Fatalf("handshake refused: %s", d.Message)
	}
	if d := g.Response(ctx, []byte(wsAccept)); !d.Allowed {
		t.Fatalf("101 refused: %s", d.Message)
	}
	return g
}

// held sends each frame on its own and fails if any of them was released.
func held(t *testing.T, g *gate.Gate, frames ...[]byte) {
	t.Helper()
	for i, f := range frames {
		d := g.Request(context.Background(), f)
		if !d.Allowed || len(d.Payload) != 0 {
			t.Fatalf("frame %d: allowed %v with %d bytes to forward, want held: %s", i, d.Allowed, len(d.Payload), d.Message)
		}
	}
}

// refused fails unless d denies by rule. A denial forwards nothing, whatever
// its Payload: the proxy pins that end to end.
func refused(t *testing.T, d gate.Decision, rule string) {
	t.Helper()
	if d.Allowed || d.Rule != rule {
		t.Fatalf("allowed %v rule %q, want a %s refusal: %s", d.Allowed, d.Rule, rule, d.Message)
	}
}

func TestAWebSocketMessageIsHeldUntilWhole(t *testing.T) {
	ctx := context.Background()

	t.Run("released whole", func(t *testing.T) {
		g := upgraded(t, 0, httpcodec.Options{})
		first, ping, last := wsFrame(false, 0x1, "hello "), wsFrame(true, 0x9, ""), wsFrame(true, 0x0, "world")
		held(t, g, first, ping)
		d := g.Request(ctx, last)
		if !d.Allowed {
			t.Fatalf("the completed message was refused: %s", d.Message)
		}
		if want := bytes.Join([][]byte{first, ping, last}, nil); !bytes.Equal(d.Payload, want) {
			t.Errorf("Payload = % x, want every frame in order: % x", d.Payload, want)
		}
	})

	t.Run("denied whole", func(t *testing.T) {
		g := upgraded(t, 0, httpcodec.Options{})
		held(t, g, wsFrame(false, 0x1, "drop ta"))
		d := g.Request(ctx, wsFrame(true, 0x0, "ble users"))
		refused(t, d, "no-drop-table")
	})

	// The gate forwards a stream it cannot parse, so an error must not
	// release fragments nobody judged.
	t.Run("malformed while held", func(t *testing.T) {
		g := upgraded(t, 0, httpcodec.Options{})
		held(t, g, wsFrame(false, 0x1, "drop ta"))
		d := g.Request(ctx, wsFrame(true, 0x3, ""))
		refused(t, d, "stream-unsafe")
	})

	// The same read can carry a whole message behind the bad frame.
	t.Run("malformed ahead of a message", func(t *testing.T) {
		g := upgraded(t, 0, httpcodec.Options{})
		d := g.Request(ctx, append(wsFrame(true, 0x1, "a\xffb"), wsFrame(true, 0x1, "drop table users")...))
		refused(t, d, "stream-unsafe")
	})

	t.Run("past the buffer", func(t *testing.T) {
		g := upgraded(t, 1024, httpcodec.Options{})
		d := g.Request(ctx, wsFrame(false, 0x1, strings.Repeat("a", 400)))
		for range 4 {
			if !d.Allowed {
				break
			}
			if len(d.Payload) != 0 {
				t.Fatalf("%d bytes of an incomplete message were released", len(d.Payload))
			}
			d = g.Request(ctx, wsFrame(false, 0x0, strings.Repeat("b", 400)))
		}
		refused(t, d, "stream-unsafe")
	})
}
