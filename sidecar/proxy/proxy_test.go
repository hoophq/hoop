package proxy_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/audit"
	_ "github.com/hoophq/hoop/sidecar/codec/all"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/proxy"
	"github.com/hoophq/hoop/sidecar/session"
)

func pgQuery(sql string) []byte {
	var b bytes.Buffer
	b.WriteByte('Q')
	binary.Write(&b, binary.BigEndian, uint32(len(sql)+5))
	b.WriteString(sql)
	b.WriteByte(0)
	return b.Bytes()
}

// echoUpstream accepts one connection and echoes everything back, recording
// what it received so a test can assert nothing was forwarded on a denial.
type echoUpstream struct {
	ln       net.Listener
	mu       sync.Mutex
	received []byte
	reply    []byte
}

func newEchoUpstream(t *testing.T, reply []byte) *echoUpstream {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("upstream listen: %v", err)
	}
	u := &echoUpstream{ln: ln, reply: reply}
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
						out := u.reply
						if out == nil {
							out = buf[:n]
						}
						if _, werr := c.Write(out); werr != nil {
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

func (u *echoUpstream) addr() string { return u.ln.Addr().String() }

func (u *echoUpstream) got() []byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]byte(nil), u.received...)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func startServer(t *testing.T, cfg proxy.Config) *proxy.Server {
	t.Helper()
	cfg.Listen = "127.0.0.1:0"
	if cfg.Logger == nil {
		cfg.Logger = quietLogger()
	}
	s, err := proxy.NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	go func() {
		close(ready)
		_ = s.Serve(ctx)
	}()
	<-ready

	// Serve binds asynchronously; wait for the address.
	deadline := time.Now().Add(2 * time.Second)
	for s.Addr() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Addr() == nil {
		t.Fatal("server did not bind")
	}

	t.Cleanup(func() { cancel(); s.Close() })
	return s
}

func denyDrops(t *testing.T) *policy.Rules {
	t.Helper()
	r, err := policy.NewRules([]policy.Rule{{
		Name:       "no-destructive",
		Type:       policy.MatchOperation,
		Operations: []inspect.Operation{inspect.OpDrop},
		Message:    "destructive statements are not permitted on appdb",
	}})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}
	return r
}

func TestAllowedTrafficIsRelayed(t *testing.T) {
	up := newEchoUpstream(t, nil)
	sink := audit.NewMemorySink(64)

	srv := startServer(t, proxy.Config{
		Upstream:   up.addr(),
		Protocol:   inspect.Postgres,
		Connection: "appdb",
		Policy:     denyDrops(t),
		Audit:      sink,
	})

	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	msg := pgQuery("SELECT name FROM customers")
	if _, err := c.Write(msg); err != nil {
		t.Fatalf("write: %v", err)
	}

	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if !bytes.Equal(buf, msg) {
		t.Error("relayed bytes differ from what was sent")
	}
	if !bytes.Equal(up.got(), msg) {
		t.Error("upstream received different bytes")
	}
}

// The load-bearing assertion: on a denial the upstream must receive NOTHING.
func TestDeniedStatementNeverReachesUpstream(t *testing.T) {
	up := newEchoUpstream(t, nil)
	sink := audit.NewMemorySink(64)

	srv := startServer(t, proxy.Config{
		Upstream:   up.addr(),
		Protocol:   inspect.Postgres,
		Connection: "appdb",
		Policy:     denyDrops(t),
		Audit:      sink,
		DenyWriter: proxy.ProtocolDenyWriter{},
	})

	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	if _, err := c.Write(pgQuery("DROP TABLE customers")); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The client must receive a Postgres ErrorResponse carrying the message.
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 512)
	n, _ := c.Read(buf)
	got := string(buf[:n])

	if n == 0 {
		t.Fatal("connection closed with no explanation; the user cannot tell why")
	}
	if buf[0] != 'E' {
		t.Errorf("first byte = %q, want 'E' (ErrorResponse)", buf[0])
	}
	if !strings.Contains(got, "destructive statements are not permitted on appdb") {
		t.Errorf("error frame does not carry the operator message: %q", got)
	}

	// Give the relay a moment, then confirm the upstream saw nothing.
	time.Sleep(100 * time.Millisecond)
	if len(up.got()) != 0 {
		t.Errorf("upstream received %d bytes on a denied statement", len(up.got()))
	}

	// And the violation is on the record.
	var violations int
	for _, ev := range sink.Events() {
		if ev.Kind == audit.KindViolation {
			violations++
			if ev.Message != "destructive statements are not permitted on appdb" {
				t.Errorf("violation message = %q", ev.Message)
			}
		}
	}
	if violations != 1 {
		t.Errorf("violations recorded = %d, want 1", violations)
	}
}

func TestSessionAuditLifecycle(t *testing.T) {
	up := newEchoUpstream(t, nil)
	sink := audit.NewMemorySink(64)

	srv := startServer(t, proxy.Config{
		Upstream:   up.addr(),
		Protocol:   inspect.Postgres,
		Connection: "appdb",
		Audit:      sink,
		IdentityFn: func(net.Conn) session.Identity {
			return session.Identity{Subject: "alice@example.com"}
		},
	})

	c, _ := net.Dial("tcp", srv.Addr().String())
	c.Write(pgQuery("SELECT 1"))
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	io.ReadFull(c, make([]byte, len(pgQuery("SELECT 1"))))
	c.Close()

	// Wait for the session-end event.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if hasKind(sink.Events(), audit.KindSessionEnd) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	events := sink.Events()
	if !hasKind(events, audit.KindSessionStart) {
		t.Error("no session_start recorded")
	}
	if !hasKind(events, audit.KindStatement) {
		t.Error("no statement recorded")
	}
	if !hasKind(events, audit.KindSessionEnd) {
		t.Error("no session_end recorded")
	}
	for _, ev := range events {
		if ev.Principal != "alice@example.com" {
			t.Errorf("event %s has principal %q; identity must reach the audit trail",
				ev.Kind, ev.Principal)
		}
	}
}

func hasKind(events []audit.Event, k audit.Kind) bool {
	for _, e := range events {
		if e.Kind == k {
			return true
		}
	}
	return false
}

// Response-side masking must rewrite bytes on the way back to the client.
type emailMasker struct{}

func (emailMasker) Mask(data []byte) ([]byte, []string, int) {
	const target = "ada@example.com"
	n := bytes.Count(data, []byte(target))
	if n == 0 {
		return data, nil, 0
	}
	return bytes.ReplaceAll(data, []byte(target), []byte("[REDACTED]")), []string{"email"}, n
}

func (m emailMasker) MaskCell(_ string, value []byte) ([]byte, []string, int) {
	return m.Mask(value)
}

// Masking runs on HTTP, which carries body length in a header the relay
// forwards as a unit. The gate refuses it on the length-prefixed binary
// protocols, where substitution would desynchronize the client. See
// gate.TestMaskingIsRefusedOnLengthPrefixedProtocols.
func TestResponseMasking(t *testing.T) {
	body := "ada@example.com"
	resp := "HTTP/1.1 200 OK\r\nContent-Length: 15\r\n\r\n" + body
	up := newEchoUpstream(t, []byte(resp))
	sink := audit.NewMemorySink(64)

	srv := startServer(t, proxy.Config{
		Upstream: up.addr(),
		Protocol: inspect.HTTP,
		Audit:    sink,
		Masker:   emailMasker{},
	})

	c, _ := net.Dial("tcp", srv.Addr().String())
	defer c.Close()
	c.Write([]byte("GET /users HTTP/1.1\r\nHost: h\r\n\r\n"))

	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 256)
	n, _ := c.Read(buf)
	got := string(buf[:n])

	if strings.Contains(got, "ada@example.com") {
		t.Errorf("the sensitive value reached the client: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("response was not masked: %q", got)
	}
}

func TestUpstreamUnreachableIsRecorded(t *testing.T) {
	sink := audit.NewMemorySink(16)
	srv := startServer(t, proxy.Config{
		// Port 1 on loopback refuses immediately.
		Upstream:    "127.0.0.1:1",
		Protocol:    inspect.Postgres,
		Audit:       sink,
		DialTimeout: 500 * time.Millisecond,
	})

	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if hasKind(sink.Events(), audit.KindError) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("an unreachable upstream produced no error event")
}

func TestMaxConnsRefusesRatherThanQueues(t *testing.T) {
	up := newEchoUpstream(t, nil)
	srv := startServer(t, proxy.Config{
		Upstream: up.addr(),
		Protocol: inspect.Postgres,
		MaxConns: 1,
	})

	first, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	defer first.Close()
	first.Write(pgQuery("SELECT 1"))
	first.SetReadDeadline(time.Now().Add(2 * time.Second))
	io.ReadFull(first, make([]byte, 6))

	second, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		return // refused at connect is also acceptable
	}
	defer second.Close()

	// The server accepts then closes at once when at capacity, so a read
	// returns EOF rather than data.
	second.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, err := second.Read(make([]byte, 16)); err == nil && n > 0 {
		t.Error("a connection beyond MaxConns was served")
	}
}

func TestStatsCountConnectionsAndDenials(t *testing.T) {
	up := newEchoUpstream(t, nil)
	srv := startServer(t, proxy.Config{
		Upstream:   up.addr(),
		Protocol:   inspect.Postgres,
		Policy:     denyDrops(t),
		DenyWriter: proxy.ProtocolDenyWriter{},
	})

	c, _ := net.Dial("tcp", srv.Addr().String())
	c.Write(pgQuery("DROP TABLE t"))
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	c.Read(make([]byte, 256))
	c.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, total, denied := srv.Stats(); total >= 1 && denied >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, total, denied := srv.Stats()
	t.Errorf("Stats = (total=%d, denied=%d), want at least (1, 1)", total, denied)
}

// A config error must surface at startup, not on the first connection.
func TestNewServerValidatesConfig(t *testing.T) {
	cases := map[string]proxy.Config{
		"no listen":   {Upstream: "h:1", Protocol: inspect.Postgres},
		"no upstream": {Listen: ":0", Protocol: inspect.Postgres},
		"no protocol": {Listen: ":0", Upstream: "h:1"},
		"bad protocol": {
			Listen: ":0", Upstream: "h:1", Protocol: "oracle",
		},
	}
	for name, cfg := range cases {
		if _, err := proxy.NewServer(cfg); err == nil {
			t.Errorf("%s: NewServer accepted an invalid config", name)
		}
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	up := newEchoUpstream(t, nil)
	srv := startServer(t, proxy.Config{
		Upstream: up.addr(),
		Protocol: inspect.Postgres,
	})
	if err := srv.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// --- deny frames ---------------------------------------------------------

func TestPostgresErrorFrame(t *testing.T) {
	frame := proxy.PostgresError("nope")

	if frame[0] != 'E' {
		t.Fatalf("tag = %q, want 'E'", frame[0])
	}
	declared := binary.BigEndian.Uint32(frame[1:5])
	if int(declared) != len(frame)-1 {
		t.Errorf("declared length %d does not match frame length %d", declared, len(frame)-1)
	}
	if !bytes.Contains(frame, []byte("nope")) {
		t.Error("message missing from the frame")
	}
	// FATAL, not ERROR: the connection is closing, and ERROR would leave
	// psql waiting for a ReadyForQuery that never arrives.
	if !bytes.Contains(frame, []byte("FATAL")) {
		t.Error("severity is not FATAL")
	}
	if frame[len(frame)-1] != 0 {
		t.Error("field list is not NUL terminated")
	}
}

func TestHTTPForbiddenFrame(t *testing.T) {
	frame := string(proxy.HTTPForbidden("nope"))

	if !strings.HasPrefix(frame, "HTTP/1.1 403 Forbidden\r\n") {
		t.Errorf("status line = %q", strings.SplitN(frame, "\r\n", 2)[0])
	}
	if !strings.Contains(frame, "Content-Length: 5\r\n") {
		t.Error("Content-Length is wrong or missing")
	}
	// Without Connection: close a keep-alive client waits for a second
	// response that never comes.
	if !strings.Contains(frame, "Connection: close") {
		t.Error("Connection: close missing")
	}
	if !strings.HasSuffix(frame, "nope\n") {
		t.Errorf("body = %q", frame)
	}
}

func TestDenyWriterDispatch(t *testing.T) {
	w := proxy.ProtocolDenyWriter{}
	for _, proto := range []inspect.Protocol{
		inspect.Postgres, inspect.HTTP,
	} {
		if len(w.Deny(proto, inspect.FromClient, "x")) == 0 {
			t.Errorf("%s produced no deny frame", proto)
		}
	}
	// A protocol with no shipped codec gets no frame: without a decoder
	// there is no statement to explain a denial about, and emitting bytes a
	// driver misparses is worse than closing.
	if w.Deny(inspect.Protocol("mongodb"), inspect.FromClient, "x") != nil {
		t.Error("an unsupported protocol produced a deny frame")
	}
}

func TestDenyFrameFallsBackToAGenericMessage(t *testing.T) {
	frame := proxy.ProtocolDenyWriter{}.Deny(inspect.Postgres, inspect.FromClient, "")
	if !bytes.Contains(frame, []byte("denied by policy")) {
		t.Error("an empty message produced no fallback text")
	}
}

// --- unix sockets ---------------------------------------------------------

// udsPath returns a socket path short enough for the platform's sun_path
// limit (104 bytes on darwin, 108 on linux). t.TempDir() under the default
// TMPDIR can exceed it on macOS, and the failure reads as "invalid argument".
func udsPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "uds")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// startUnixServer is startServer for a listener whose address the caller
// chose. startServer overwrites Listen with 127.0.0.1:0, which is right for
// an ephemeral TCP port and wrong for a socket path.
func startUnixServer(t *testing.T, cfg proxy.Config) *proxy.Server {
	t.Helper()
	if cfg.Logger == nil {
		cfg.Logger = quietLogger()
	}
	s, err := proxy.NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = s.Serve(ctx) }()

	// Serve binds asynchronously; wait for the socket to answer.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, derr := net.Dial("unix", cfg.Listen); derr == nil {
			c.Close()
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	t.Cleanup(func() { cancel(); s.Close() })
	return s
}

// A unix socket is the tightest way to put Envoy in front of the relay:
// filesystem permissions decide who connects, and nothing on the network can
// reach it at all.
func TestUnixSocketRelaysTraffic(t *testing.T) {
	up := newEchoUpstream(t, nil)
	sock := udsPath(t)

	startUnixServer(t, proxy.Config{
		Network:    "unix",
		Listen:     sock,
		Upstream:   up.addr(),
		Protocol:   inspect.Postgres,
		Connection: "appdb",
		Policy:     denyDrops(t),
		Audit:      audit.NewMemorySink(64),
		DenyWriter: proxy.ProtocolDenyWriter{},
	})

	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial unix: %v", err)
	}
	defer c.Close()

	if _, err := c.Write(pgQuery("SELECT 1")); err != nil {
		t.Fatalf("write: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !bytes.Contains(up.got(), []byte("SELECT 1")) {
		time.Sleep(2 * time.Millisecond)
	}
	if got := up.got(); !bytes.Contains(got, []byte("SELECT 1")) {
		t.Errorf("upstream received %q, want it to contain SELECT 1", got)
	}
}

// Policy must not weaken because the transport changed. A denial over a unix
// socket has to stop the statement exactly as it does over TCP.
func TestUnixSocketStillDenies(t *testing.T) {
	up := newEchoUpstream(t, nil)
	sock := udsPath(t)

	startUnixServer(t, proxy.Config{
		Network:    "unix",
		Listen:     sock,
		Upstream:   up.addr(),
		Protocol:   inspect.Postgres,
		Connection: "appdb",
		Policy:     denyDrops(t),
		Audit:      audit.NewMemorySink(64),
		DenyWriter: proxy.ProtocolDenyWriter{},
	})

	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial unix: %v", err)
	}
	defer c.Close()

	if _, err := c.Write(pgQuery("DROP TABLE customers")); err != nil {
		t.Fatalf("write: %v", err)
	}

	reply := make([]byte, 256)
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _ := c.Read(reply)
	if n == 0 || reply[0] != 'E' {
		t.Errorf("expected a pgwire ErrorResponse, got %q", reply[:n])
	}
	if len(up.got()) > 0 {
		t.Errorf("the denied statement reached the upstream: %q", up.got())
	}
}

// Go unlinks the socket on an orderly close, so only a SIGKILL, an OOM kill
// or `docker kill` leaves the file behind. Without this reclaim every restart
// after one of those fails with "bind: address already in use" until a human
// deletes a file, which is a bad way to spend an outage.
func TestUnixSocketReclaimsAStaleFile(t *testing.T) {
	up := newEchoUpstream(t, nil)
	sock := udsPath(t)

	// A leftover socket file with nothing listening on it.
	stale, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	if uln, ok := stale.(*net.UnixListener); ok {
		uln.SetUnlinkOnClose(false) // reproduce the unclean exit
	}
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("test setup left no stale socket: %v", err)
	}

	startUnixServer(t, proxy.Config{
		Network:    "unix",
		Listen:     sock,
		Upstream:   up.addr(),
		Protocol:   inspect.Postgres,
		Connection: "appdb",
		Policy:     denyDrops(t),
		Audit:      audit.NewMemorySink(64),
	})

	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial after reclaim: %v", err)
	}
	c.Close()
}

// The reclaim must not steal a socket someone is answering on. Two relays
// sharing one path would split a client's connections between them at random,
// which is worse than refusing to start.
func TestUnixSocketRefusesToStealALiveSocket(t *testing.T) {
	up := newEchoUpstream(t, nil)
	sock := udsPath(t)

	startUnixServer(t, proxy.Config{
		Network:    "unix",
		Listen:     sock,
		Upstream:   up.addr(),
		Protocol:   inspect.Postgres,
		Connection: "appdb",
		Policy:     denyDrops(t),
		Audit:      audit.NewMemorySink(64),
	})

	second, err := proxy.NewServer(proxy.Config{
		Network:    "unix",
		Listen:     sock,
		Upstream:   up.addr(),
		Protocol:   inspect.Postgres,
		Connection: "appdb",
		Policy:     denyDrops(t),
		Audit:      audit.NewMemorySink(64),
		Logger:     quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	err = second.Serve(context.Background())
	if err == nil {
		t.Fatal("the second server bound a socket another process owns")
	}
	if !strings.Contains(err.Error(), "live socket") {
		t.Errorf("error %q does not name the conflict", err)
	}

	// The original must still be serving.
	c, derr := net.Dial("unix", sock)
	if derr != nil {
		t.Fatalf("the first server stopped serving: %v", derr)
	}
	c.Close()
}
