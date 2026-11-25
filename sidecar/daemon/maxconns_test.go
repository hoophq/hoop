package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

func TestConnLimitResolvesMaxConns(t *testing.T) {
	for _, tc := range []struct{ maxConns, want int }{
		{0, DefaultMaxConns}, // unset
		{MaxConnsUnlimited, 0},
		{-7, 0}, // read as -1, as every release before the default did
		{1, 1},
		{500, 500},
	} {
		if got := (ListenerConfig{MaxConns: tc.maxConns}).connLimit(); got != tc.want {
			t.Errorf("max_conns %d: limit %d, want %d", tc.maxConns, got, tc.want)
		}
	}
	if DefaultMaxConns != 200 {
		t.Errorf("DefaultMaxConns = %d, want 200", DefaultMaxConns)
	}
}

// The control plane refuses a value below -1. An mcp lane keeps its own
// check, which refuses only a positive value.
func TestMaxConnsProblemsNamesValuesBelowMinusOne(t *testing.T) {
	got := MaxConnsProblems([]ListenerConfig{
		{Name: "unset", Protocol: "postgres"},
		{Name: "unlimited", Protocol: "postgres", MaxConns: -1},
		{Name: "explicit", Protocol: "postgres", MaxConns: 50},
		{Name: "typo", Protocol: "postgres", MaxConns: -2},
		{Name: "tools", Protocol: "mcp", MaxConns: -2},
	})
	if len(got) != 1 || !strings.HasPrefix(got[0], "typo: max_conns -2 is below -1") {
		t.Fatalf("problems = %q, want one for typo", got)
	}
}

// A sidecar does not refuse a stored value below -1: the release before it
// booted with one. It runs the lane unlimited and says so.
func TestASidecarReadsMaxConnsBelowMinusOneAsUnlimited(t *testing.T) {
	cfg, err := LoadConfigBytes([]byte(`{"listeners":[{"name":"appdb","protocol":"postgres",` +
		`"listen":"127.0.0.1:0","upstream":"127.0.0.1:1","max_conns":-5}]}`))
	if err != nil {
		t.Fatalf("a stored max_conns -5 was refused: %v", err)
	}
	lanes, err := Validate(cfg, nil)
	if err != nil {
		t.Fatalf("-validate refused max_conns -5: %v", err)
	}
	if notes := strings.Join(lanes[0].Notes, "\n"); !strings.Contains(notes, "max_conns -5 is below -1 and reads as -1, unlimited") {
		t.Errorf("notes do not say how -5 reads:\n%s", notes)
	}
	if got := cfg.Listeners[0].connLimit(); got != 0 {
		t.Errorf("limit %d, want 0 (none)", got)
	}
}

// An mcp lane does not change: unset and -1 load, a positive value is
// refused, and nothing notes a value it never reads.
func TestAnMCPLaneKeepsItsMaxConnsRule(t *testing.T) {
	const ok = `"name":"m","protocol":"mcp","listen":"127.0.0.1:1","upstream":"https://h/mcp"`
	for _, extra := range []string{``, `,"max_conns":-1`, `,"max_conns":-3`} {
		if p := mcpProblems(t, `{`+ok+extra+`}`); p != "" {
			t.Errorf("%q refused: %s", extra, p)
		}
	}
	if p := mcpProblems(t, `{`+ok+`,"max_conns":200}`); !strings.Contains(p, "max_conns does not apply to an mcp listener") {
		t.Errorf("max_conns 200 on an mcp lane: %q", p)
	}
	if n := maxConnsNote(ListenerConfig{Protocol: "mcp", MaxConns: -3}); n != "" {
		t.Errorf("an mcp lane got a note: %q", n)
	}
}

func dialHeld(t *testing.T, addr string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// closedAtOnce reports whether the relay closed c instead of serving it.
func closedAtOnce(c net.Conn, wait time.Duration) bool {
	_ = c.SetReadDeadline(time.Now().Add(wait))
	_, err := c.Read(make([]byte, 1))
	var ne net.Error
	return err != nil && !(errors.As(err, &ne) && ne.Timeout())
}

// limitCases is what each server builder must do with max_conns: limit is
// the connection count it admits, 0 for none (then 201 are dialed and all
// must be served).
var limitCases = []struct {
	name            string
	maxConns, limit int
}{
	{"unset", 0, DefaultMaxConns},
	{"explicit", 3, 3},
	{"unlimited", MaxConnsUnlimited, 0},
	{"below -1", -5, 0},
}

// held is how many connections a case opens before the one it checks.
func held(limit int) int {
	if limit == 0 {
		return DefaultMaxConns
	}
	return limit
}

// The relay admits the limit and refuses the next connection at accept,
// with the log line an operator searches for. Unset is 200; -1 and below
// admit connection 201.
func TestARelayLaneWithMaxConnsUnsetRefusesConnection201(t *testing.T) {
	for _, tc := range limitCases {
		t.Run(tc.name, func(t *testing.T) {
			logs := &syncBuffer{}
			ln := lane{name: "appdb", cfg: ListenerConfig{Name: "appdb", Protocol: "postgres",
				Listen: "127.0.0.1:0", Upstream: holdingUpstream(t), MaxConns: tc.maxConns}}
			ln.codecFactory = laneCodecFactory(ln.cfg, nil)
			srv, err := buildServer(ln, AuditConfig{}, nil, nil, nil, slog.New(slog.NewTextHandler(logs, nil)))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			go func() { _ = srv.Serve(ctx) }()
			addr := waitForAddr(t, srv)

			n := held(tc.limit)
			for range n {
				dialHeld(t, addr)
			}
			deadline := time.Now().Add(5 * time.Second)
			for active, _, _ := srv.Stats(); active < int64(n); active, _, _ = srv.Stats() {
				if time.Now().After(deadline) {
					t.Fatalf("%d connections active, want %d", active, n)
				}
				time.Sleep(5 * time.Millisecond)
			}

			refused := tc.limit > 0
			next := dialHeld(t, addr)
			if got := closedAtOnce(next, 500*time.Millisecond); got != refused {
				t.Fatalf("connection %d closed at once = %v, want %v", n+1, got, refused)
			}
			log := logs.String()
			refusedLine := strings.Contains(log, "connection refused, at capacity") &&
				strings.Contains(log, fmt.Sprintf("max_conns=%d", tc.limit))
			if refusedLine != refused {
				t.Errorf("refusal logged = %v, want %v:\n%s", refusedLine, refused, log)
			}
		})
	}
}

// h2Hello is a client's HTTP/2 connection preface and an empty SETTINGS
// frame. A gRPC lane that accepted the connection answers with SETTINGS.
var h2Hello = append([]byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"), 0, 0, 0, 4, 0, 0, 0, 0, 0)

// answered reports whether the server on c spoke within wait, after hello.
func answered(t *testing.T, c net.Conn, hello []byte, wait time.Duration) bool {
	t.Helper()
	if len(hello) > 0 {
		if _, err := c.Write(hello); err != nil {
			t.Fatal(err)
		}
	}
	_ = c.SetReadDeadline(time.Now().Add(wait))
	n, err := io.ReadAtLeast(c, make([]byte, 4), 4)
	return err == nil && n >= 4
}

// libhoop's grpc and ssh servers take a slot BEFORE they accept, so the
// connection past the limit waits in the kernel's accept queue instead of
// being closed. These tests read that: the limit answered, the next one
// silent until one closes. Without a limit, connection 201 is answered at
// once.
func assertEndpointLimit(t *testing.T, start func(t *testing.T, maxConns int) string, hello []byte) {
	t.Helper()
	for _, tc := range limitCases {
		t.Run(tc.name, func(t *testing.T) {
			addr := start(t, tc.maxConns)
			n := held(tc.limit)
			open := make([]net.Conn, 0, n)
			for i := range n {
				c := dialHeld(t, addr)
				if !answered(t, c, hello, 5*time.Second) {
					t.Fatalf("connection %d was not answered", i+1)
				}
				open = append(open, c)
			}
			limited := tc.limit > 0
			next := dialHeld(t, addr)
			if got := answered(t, next, hello, 500*time.Millisecond); got == limited {
				t.Fatalf("connection %d answered = %v, want %v", n+1, got, !limited)
			}
			if !limited {
				return
			}
			// One slot frees, and the waiting connection is accepted.
			_ = open[0].Close()
			_ = next.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := io.ReadAtLeast(next, make([]byte, 4), 4); err != nil {
				t.Fatalf("connection %d was not answered after a slot freed: %v", n+1, err)
			}
		})
	}
}

func TestAGRPCLaneWithMaxConnsUnsetHoldsConnection201(t *testing.T) {
	assertEndpointLimit(t, func(t *testing.T, maxConns int) string {
		srv, _, err := buildGRPCServer(lane{name: "ledger", cfg: ListenerConfig{Name: "ledger", Protocol: "grpc",
			Listen: "127.0.0.1:0", Upstream: "127.0.0.1:1", MaxConns: maxConns}},
			AuditConfig{}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		addr, stop := startGRPCTestServer(t, srv)
		t.Cleanup(stop)
		return addr
	}, h2Hello)
}

func TestAnSSHLaneWithMaxConnsUnsetHoldsConnection201(t *testing.T) {
	assertEndpointLimit(t, func(t *testing.T, maxConns int) string {
		hostKey, trustedCA := writeSSHKeyMaterial(t)
		srv, _, err := buildSSHServer(lane{name: "jump", cfg: ListenerConfig{Name: "jump", Protocol: "ssh",
			Listen: "127.0.0.1:0", MaxConns: maxConns, SSH: &SSHConfig{HostKey: hostKey, TrustedCA: trustedCA}}},
			AuditConfig{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		go func() { _ = srv.Serve(ctx) }()
		t.Cleanup(func() { cancel(); _ = srv.Close() })
		return waitForAddr(t, srv)
	}, nil)
}

// A reload that adds a lane with max_conns below -1 warns once, as startup
// does, and runs the lane unlimited. The next pass over the same document
// is unchanged and says nothing.
func TestAReloadAddingALaneBelowMinusOneWarns(t *testing.T) {
	base := withUpstream(t, reloadBase)
	rl, buf, rt := testRunningReloader(t, base)
	added := fmt.Sprintf(`{"name": "second", "protocol": "postgres", "listen": %q, "upstream": %q, "max_conns": -2}, `,
		freeTCPAddr(t), holdingUpstream(t))
	drifted := editJSON(t, base, `"listeners": [{`, `"listeners": [`+added+`{`)
	if got := handleWith(rl, buf, drifted); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, buf)
	}
	const warning = `msg="max_conns -2 is below -1 and reads as -1, unlimited; write -1" listener=second`
	if n := strings.Count(buf.String(), warning); n != 1 {
		t.Fatalf("warning logged %d times, want 1:\n%s", n, buf)
	}
	addr := laneAddr(t, rt, "second")
	for range DefaultMaxConns {
		dialHeld(t, addr)
	}
	if closedAtOnce(dialHeld(t, addr), 500*time.Millisecond) {
		t.Error("connection 201 was refused on a lane with max_conns -2")
	}
	if got := handleWith(rl, buf, drifted); got != reloadUnchanged {
		t.Fatalf("second pass = %v, want unchanged", got)
	}
	if n := strings.Count(buf.String(), warning); n != 1 {
		t.Errorf("an unchanged pass logged the warning again (%d times)", n)
	}
}
