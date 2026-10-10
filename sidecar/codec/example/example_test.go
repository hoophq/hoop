package example_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/codec/example"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/proxy"
	"github.com/hoophq/hoop/sidecar/session"
)

// registerOnce puts x-example in the registry for this test binary only.
// The schema step needs it; the gate path and a proxy with a CodecFactory
// do not, and the gate path runs first to prove that. registered remembers
// that it fired: `-count=2` reruns the test in the same process, where the
// registry already holds the protocol.
var (
	registerOnce sync.Once
	registered   bool
)

func frame(text string) []byte {
	return append([]byte{byte(len(text))}, text...)
}

func denyDrop(t *testing.T) *policy.Rules {
	t.Helper()
	r, err := policy.NewRules([]policy.Rule{{
		Name:    "no-drop",
		Type:    policy.MatchDenyWords,
		Words:   []string{"drop"},
		Message: "drop is not allowed here",
	}})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}
	return r
}

// schemaLabel returns the label the listener form would show for p, and
// whether the schema lists p at all.
func schemaLabel(t *testing.T, p string) (string, bool) {
	t.Helper()
	raw, err := daemon.ListenerSchema()
	if err != nil {
		t.Fatalf("ListenerSchema: %v", err)
	}
	var doc struct {
		Protocols []struct {
			Value string `json:"value"`
			Label string `json:"label"`
		} `json:"protocols"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	for _, opt := range doc.Protocols {
		if opt.Value == p {
			return opt.Label, true
		}
	}
	return "", false
}

// One test, in this order, because the registry is process state: the
// steps before Register prove a foreign codec needs none of it, and the
// steps after prove what registering buys. Split into top-level tests the
// "absent" assertion would depend on run order.
func TestExampleProtocolEndToEnd(t *testing.T) {
	t.Run("schema lists only registered protocols", func(t *testing.T) {
		if registered {
			t.Skip("an earlier run of this binary registered x-example")
		}
		if label, listed := schemaLabel(t, string(example.Protocol)); listed {
			t.Fatalf("x-example (%q) is in the schema before anything registered it", label)
		}
	})

	t.Run("gate denies through CodecFactory without the registry", func(t *testing.T) {
		sess := session.New(example.Protocol, session.Identity{Subject: "alice"})
		g, err := gate.New(sess, gate.Config{
			Protocol:     example.Protocol,
			Policy:       denyDrop(t),
			CodecFactory: example.New,
		})
		if err != nil {
			t.Fatalf("gate.New: %v", err)
		}
		ctx := context.Background()

		d := g.Request(ctx, frame("hello world"))
		if !d.Allowed || len(d.Statements) != 1 {
			t.Fatalf("plain text: allowed=%v statements=%d, want allowed with one statement", d.Allowed, len(d.Statements))
		}
		if got := d.Statements[0]; got.Text != "hello world" || got.Operation != inspect.OpOther ||
			got.Metadata["x-example.verb"] != "TEXT" || got.Protocol != example.Protocol {
			t.Errorf("decoded statement = %+v", got)
		}

		d = g.Request(ctx, frame("please DROP everything"))
		if d.Allowed {
			t.Fatal("the gate allowed a text carrying the deny word")
		}
		if d.Rule != "no-drop" || d.Message != "drop is not allowed here" {
			t.Errorf("denial rule=%q message=%q", d.Rule, d.Message)
		}
		if d.DeniedStatement == nil || d.DeniedStatement.Text != "please DROP everything" {
			t.Errorf("DeniedStatement = %v", d.DeniedStatement)
		}

		// The gate renders the denial in the codec's own frame.
		got, ok := g.DenyFrame(inspect.FromClient, d.Message)
		if !ok {
			t.Fatal("the gate did not find the codec's DenyFramer")
		}
		want := append([]byte{0xFF, byte(len(d.Message))}, d.Message...)
		if !bytes.Equal(got, want) {
			t.Errorf("deny frame = %x, want %x", got, want)
		}

		// The gate reassembles a split message and judges it once.
		whole := frame("split across reads")
		if d := g.Request(ctx, whole[:5]); !d.Allowed || len(d.Statements) != 0 {
			t.Fatalf("partial message: allowed=%v statements=%d", d.Allowed, len(d.Statements))
		}
		if d := g.Request(ctx, whole[5:]); !d.Allowed || len(d.Statements) != 1 || d.Statements[0].Text != "split across reads" {
			t.Fatalf("rest of message: allowed=%v statements=%v", d.Allowed, d.Statements)
		}
	})

	t.Run("a codec without Reframer cannot mask", func(t *testing.T) {
		if gate.MaskSupportedBy(example.New) {
			t.Fatal("MaskSupportedBy reports masking on a codec that cannot re-frame")
		}
	})

	registerOnce.Do(func() { example.Register(); registered = true })

	t.Run("schema labels a registered codec through gate.Labeled", func(t *testing.T) {
		label, listed := schemaLabel(t, string(example.Protocol))
		if !listed {
			t.Fatal("x-example is registered but the schema does not list it")
		}
		if label != "Example" {
			t.Fatalf("label = %q, want %q from the codec's Label()", label, "Example")
		}
	})

	t.Run("proxy writes the codec's deny frame", func(t *testing.T) {
		upstream := newRecordingUpstream(t)
		srv, err := proxy.NewServer(proxy.Config{
			Listen:       "127.0.0.1:0",
			Upstream:     upstream.addr(),
			Protocol:     example.Protocol,
			Connection:   "example",
			Policy:       denyDrop(t),
			DenyWriter:   proxy.ProtocolDenyWriter{},
			CodecFactory: example.New,
			Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		if err != nil {
			t.Fatalf("NewServer: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		go func() { _ = srv.Serve(ctx) }()
		t.Cleanup(func() { cancel(); srv.Close() })
		deadline := time.Now().Add(2 * time.Second)
		for srv.Addr() == nil && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if srv.Addr() == nil {
			t.Fatal("server did not bind")
		}

		c, err := net.Dial("tcp", srv.Addr().String())
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(3 * time.Second))

		// The relay forwards allowed text and the upstream echoes it back unchanged.
		msg := frame("hello")
		if _, err := c.Write(msg); err != nil {
			t.Fatalf("write: %v", err)
		}
		echo := make([]byte, len(msg))
		if _, err := io.ReadFull(c, echo); err != nil {
			t.Fatalf("read echo: %v", err)
		}
		if !bytes.Equal(echo, msg) {
			t.Errorf("echo = %x, want %x", echo, msg)
		}

		if _, err := c.Write(frame("DROP it")); err != nil {
			t.Fatalf("write: %v", err)
		}
		// ProtocolDenyWriter knows nothing about x-example, so the frame
		// the client reads can only have come from the codec. A bare
		// close here is the failure DenyFramer exists to prevent.
		buf := make([]byte, 512)
		n, _ := c.Read(buf)
		if n == 0 {
			t.Fatal("connection closed with no deny frame")
		}
		got := buf[:n]
		if got[0] != 0xFF {
			t.Fatalf("first byte = %#x, want 0xFF (deny tag); frame %x", got[0], got)
		}
		if int(got[1]) != len("drop is not allowed here") || !strings.Contains(string(got[2:]), "drop is not allowed here") {
			t.Errorf("deny frame = %q", got)
		}

		// The denied bytes never reached the upstream.
		time.Sleep(50 * time.Millisecond)
		if received := upstream.got(); !bytes.Equal(received, msg) {
			t.Errorf("upstream received %x, want only the allowed message %x", received, msg)
		}
	})
}

// recordingUpstream echoes every byte and remembers what it saw, so a test
// can prove the relay never forwarded a denied message.
type recordingUpstream struct {
	ln       net.Listener
	mu       sync.Mutex
	received []byte
}

func newRecordingUpstream(t *testing.T) *recordingUpstream {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("upstream listen: %v", err)
	}
	u := &recordingUpstream{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 4096)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						u.mu.Lock()
						u.received = append(u.received, buf[:n]...)
						u.mu.Unlock()
						if _, werr := c.Write(buf[:n]); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return u
}

func (u *recordingUpstream) addr() string { return u.ln.Addr().String() }

func (u *recordingUpstream) got() []byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]byte(nil), u.received...)
}
