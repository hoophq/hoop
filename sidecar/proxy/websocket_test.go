package proxy_test

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	httpcodec "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// A WebSocket message a client sends in fragments used to reach the
// upstream fragment by fragment, before the gate judged the whole message:
// a refusal then came after the upstream already held its start.

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

// wsUpstream accepts the handshake once and records every byte after it.
// echoUpstream cannot stand in: it answers every read, and a second 101
// would reach the client as a frame.
type wsUpstream struct {
	ln       net.Listener
	mu       sync.Mutex
	received []byte
}

func newWSUpstream(t *testing.T) *wsUpstream {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("upstream listen: %v", err)
	}
	u := &wsUpstream{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go u.serve(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return u
}

func (u *wsUpstream) serve(c net.Conn) {
	defer c.Close()
	var head []byte
	upgraded := false
	buf := make([]byte, 4096)
	for {
		n, err := c.Read(buf)
		data := buf[:n]
		if !upgraded {
			head = append(head, data...)
			i := bytes.Index(head, []byte("\r\n\r\n"))
			if i < 0 {
				if err != nil {
					return
				}
				continue
			}
			if _, err := c.Write([]byte(wsAccept)); err != nil {
				return
			}
			upgraded = true
			data = head[i+4:]
		}
		u.mu.Lock()
		u.received = append(u.received, data...)
		u.mu.Unlock()
		if err != nil {
			return
		}
	}
}

func (u *wsUpstream) addr() string { return u.ln.Addr().String() }

func (u *wsUpstream) frames() []byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]byte(nil), u.received...)
}

// noDropTable refuses a client message whose whole body asks to drop a
// table, and records every client message body it judged.
type noDropTable struct {
	mu     sync.Mutex
	bodies []string
}

func (p *noDropTable) Evaluate(stmt inspect.Statement) policy.Verdict {
	if stmt.Direction != inspect.FromClient || stmt.Operation != inspect.OpWSMessage || stmt.HTTP == nil {
		return policy.Verdict{}
	}
	p.mu.Lock()
	p.bodies = append(p.bodies, stmt.HTTP.Body)
	p.mu.Unlock()
	if strings.Contains(stmt.HTTP.Body, "drop table") {
		return policy.Deny("no-drop-table", "dropping tables is refused on this lane")
	}
	return policy.Verdict{}
}

func (p *noDropTable) judged() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.bodies)
}

// wsLane is an http lane that decodes client bodies, so a policy can read
// each message.
func wsLane(up *wsUpstream, pol policy.Evaluator, sink audit.Sink, opts httpcodec.Options) proxy.Config {
	opts.CaptureRequestBody = true
	return proxy.Config{
		Upstream:     up.addr(),
		Protocol:     inspect.HTTP,
		Connection:   "ws",
		Policy:       pol,
		Audit:        sink,
		DenyWriter:   proxy.ProtocolDenyWriter{},
		CodecFactory: func() inspect.Codec { return httpcodec.New(opts) },
	}
}

// dialWS completes the handshake through srv. The client must read the 101
// before it sends a frame: the lane refuses bytes sent ahead of the switch.
func dialWS(t *testing.T, srv *proxy.Server) (net.Conn, *bufio.Reader) {
	t.Helper()
	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte(wsUpgrade)); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	r := bufio.NewReader(c)
	resp, err := http.ReadResponse(r, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake answer: %v %v, want 101", resp, err)
	}
	return c, r
}

// writeFrames writes each chunk on its own, pausing so the relay reads them
// apart, and fails if the upstream received more than before ahead of the
// last chunk.
func writeFrames(t *testing.T, c net.Conn, up *wsUpstream, chunks [][]byte, before []byte) {
	t.Helper()
	for i, chunk := range chunks {
		if _, err := c.Write(chunk); err != nil {
			t.Fatalf("write chunk %d: %v", i, err)
		}
		time.Sleep(100 * time.Millisecond)
		if i < len(chunks)-1 {
			// Allowed bytes may still be in flight on a slow runner; a
			// leaked fragment makes the two differ for good.
			for deadline := time.Now().Add(2 * time.Second); !bytes.Equal(up.frames(), before) && time.Now().Before(deadline); {
				time.Sleep(10 * time.Millisecond)
			}
			if got := up.frames(); !bytes.Equal(got, before) {
				t.Fatalf("after chunk %d the upstream received % x, want % x: a fragment left before its message was judged", i, got, before)
			}
		}
	}
}

func TestADeniedFragmentedWebSocketMessageReachesNoUpstream(t *testing.T) {
	hello := wsFrame(true, 0x1, "hello")
	for name, tc := range map[string]struct {
		chunks [][]byte
		before []byte // what an earlier, allowed message put upstream
		judged []string
	}{
		"two fragments": {chunks: [][]byte{
			wsFrame(false, 0x1, "drop ta"),
			wsFrame(true, 0x0, "ble users"),
		}, judged: []string{"drop table users"}},
		"a ping between": {chunks: [][]byte{
			wsFrame(false, 0x1, "drop ta"),
			wsFrame(true, 0x9, ""),
			wsFrame(false, 0x0, "ble "),
			wsFrame(true, 0x0, "users"),
		}, judged: []string{"drop table users"}},
		"behind an allowed message": {chunks: [][]byte{
			slices.Concat(hello, wsFrame(false, 0x1, "drop ta")),
			wsFrame(true, 0x0, "ble users"),
		}, before: hello, judged: []string{"hello", "drop table users"}},
	} {
		t.Run(name, func(t *testing.T) {
			up := newWSUpstream(t)
			pol := &noDropTable{}
			sink := audit.NewMemorySink(64)
			srv := startServer(t, wsLane(up, pol, sink, httpcodec.Options{}))
			c, r := dialWS(t, srv)

			writeFrames(t, c, up, tc.chunks, tc.before)

			// The relay closes the connection on a denial.
			_, _ = io.ReadAll(r)
			if got := up.frames(); !bytes.Equal(got, tc.before) {
				t.Errorf("the upstream received % x, want % x: a denied message reached it", got, tc.before)
			}
			if got := pol.judged(); !slices.Equal(got, tc.judged) {
				t.Errorf("judged %q, want %q: the policy must see each message whole", got, tc.judged)
			}
			var violations int
			for _, ev := range sink.Events() {
				if ev.Kind == audit.KindViolation && ev.Rule == "no-drop-table" {
					violations++
				}
			}
			if violations != 1 {
				t.Errorf("violations recorded = %d, want 1", violations)
			}
		})
	}
}

// The hold must not cost an allowed message any byte: it leaves whole and in
// order, the control frames between its fragments in place.
func TestAnAllowedFragmentedWebSocketMessageArrivesWholeAndInOrder(t *testing.T) {
	up := newWSUpstream(t)
	pol := &noDropTable{}
	srv := startServer(t, wsLane(up, pol, audit.NewMemorySink(64), httpcodec.Options{}))
	c, _ := dialWS(t, srv)

	chunks := [][]byte{
		wsFrame(false, 0x1, "hello "),
		wsFrame(true, 0x9, "ka"),
		wsFrame(false, 0x0, "wor"),
		wsFrame(true, 0x8, "\x03\xe8"),
		wsFrame(true, 0x0, "ld"),
	}
	writeFrames(t, c, up, chunks, nil)

	want := slices.Concat(chunks...)
	deadline := time.Now().Add(3 * time.Second)
	for !bytes.Equal(up.frames(), want) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := up.frames(); !bytes.Equal(got, want) {
		t.Fatalf("the upstream received % x, want % x", got, want)
	}
	if got := pol.judged(); !slices.Equal(got, []string{"hello world"}) {
		t.Errorf("judged %q, want the one whole message", got)
	}
}

// A message the codec cannot hold whole is refused before any of it leaves.
func TestAFragmentedWebSocketMessageOverTheBoundIsRefused(t *testing.T) {
	up := newWSUpstream(t)
	pol := &noDropTable{}
	sink := audit.NewMemorySink(64)
	srv := startServer(t, wsLane(up, pol, sink, httpcodec.Options{MaxMessageBytes: 64}))
	c, r := dialWS(t, srv)

	writeFrames(t, c, up, [][]byte{
		wsFrame(false, 0x1, strings.Repeat("a", 40)),
		wsFrame(false, 0x0, strings.Repeat("b", 40)),
	}, nil)

	_, _ = io.ReadAll(r)
	if got := up.frames(); len(got) != 0 {
		t.Errorf("the upstream received % x, want nothing", got)
	}
	if got := pol.judged(); len(got) != 0 {
		t.Errorf("judged %q, want nothing: the message never completed", got)
	}
	var refused bool
	for _, ev := range sink.Events() {
		refused = refused || ev.Kind == audit.KindError && strings.Contains(ev.Error, "MaxMessageBytes")
	}
	if !refused {
		t.Errorf("no error event names the bound: %+v", sink.Events())
	}
}
