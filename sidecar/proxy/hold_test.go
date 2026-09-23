package proxy_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// waitingPolicy stands in for a hold: it blocks every statement until the
// connection's context ends, then denies with the cause, the way the
// analyzer's hold does.
type waitingPolicy struct {
	started chan struct{}
	ended   chan error
}

func newWaitingPolicy() *waitingPolicy {
	return &waitingPolicy{started: make(chan struct{}, 1), ended: make(chan error, 1)}
}

func (p *waitingPolicy) Evaluate(stmt inspect.Statement) policy.Verdict {
	return p.EvaluateWith(stmt, nil)
}

func (p *waitingPolicy) EvaluateWith(_ inspect.Statement, ec *policy.EvalContext) policy.Verdict {
	p.started <- struct{}{}
	if ec == nil || ec.ConnCtx == nil {
		p.ended <- nil
		return policy.Deny("hold", "no connection context reached the policy")
	}
	select {
	case <-ec.ConnCtx.Done():
	case <-time.After(10 * time.Second):
		p.ended <- nil
		return policy.Deny("hold", "the wait never ended")
	}
	cause := context.Cause(ec.ConnCtx)
	p.ended <- cause
	return policy.Deny("hold", "the connection ended while waiting: "+cause.Error())
}

// cancelHonoringSink refuses a write under an ended context, as the SQLite
// store does. The hold's record is written after the connection ended, so
// this is the sink that would lose it.
type cancelHonoringSink struct{ *audit.MemorySink }

func (s cancelHonoringSink) Write(ctx context.Context, ev audit.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.MemorySink.Write(ctx, ev)
}

// A client that hangs up mid-hold ends the wait at once, and the statement's
// record still lands with the outcome and the cause. Without the read-ahead
// nothing reads the client socket while the pump sits in the gate, and the
// hold would run to its budget.
func TestAHoldEndsWhenTheClientHangsUp(t *testing.T) {
	up := newEchoUpstream(t, nil)
	pol := newWaitingPolicy()
	sink := audit.NewMemorySink(64)

	srv := startServer(t, proxy.Config{
		Upstream:   up.addr(),
		Protocol:   inspect.Postgres,
		Connection: "appdb",
		Policy:     pol,
		Audit:      cancelHonoringSink{sink},
		DenyWriter: proxy.ProtocolDenyWriter{},
	})

	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err := c.Write(pgQuery("DELETE FROM customers")); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case <-pol.started:
	case <-time.After(3 * time.Second):
		t.Fatal("the statement never reached the policy")
	}
	c.Close()

	select {
	case cause := <-pol.ended:
		if cause == nil || !strings.Contains(cause.Error(), "the client closed the connection") {
			t.Errorf("the wait ended with %v, want the client hangup as its cause", cause)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the hold outlived the client")
	}

	if got := up.got(); len(got) != 0 {
		t.Errorf("upstream received %d bytes from a statement whose client left", len(got))
	}
	waitForViolation(t, sink, "the client closed the connection")
}

// The upstream going away ends the wait too, before anything is claimed: the
// statement could not run on a dead socket, and the approval stays unspent
// for the retry.
func TestAHoldEndsWhenTheUpstreamCloses(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("upstream listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	upConns := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			upConns <- c
		}
	}()

	pol := newWaitingPolicy()
	srv := startServer(t, proxy.Config{
		Upstream:   ln.Addr().String(),
		Protocol:   inspect.Postgres,
		Connection: "appdb",
		Policy:     pol,
	})

	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	if _, err := c.Write(pgQuery("DELETE FROM customers")); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case <-pol.started:
	case <-time.After(3 * time.Second):
		t.Fatal("the statement never reached the policy")
	}
	(<-upConns).Close()

	select {
	case cause := <-pol.ended:
		if cause == nil || !strings.Contains(cause.Error(), "the upstream closed the connection") {
			t.Errorf("the wait ended with %v, want the upstream close as its cause", cause)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the hold outlived the upstream")
	}
}

// decidingPolicy stands in for a hold that settles: it blocks every request
// until the test sends the verdict a reviewer would. Responses pass.
type decidingPolicy struct {
	started chan struct{}
	verdict chan policy.Verdict
}

func (p *decidingPolicy) Evaluate(stmt inspect.Statement) policy.Verdict {
	if stmt.Direction != inspect.FromClient {
		return policy.Verdict{}
	}
	p.started <- struct{}{}
	return <-p.verdict
}

// An http request held for review reaches the upstream only once released,
// and a refusal reaches the caller as a 403 that names it.
func TestAnHTTPHoldForwardsOnlyWhatIsReleased(t *testing.T) {
	for _, tc := range []struct {
		name    string
		verdict policy.Verdict
		want    string
		forward bool
	}{
		{name: "approved", verdict: policy.Verdict{}, want: "200 OK", forward: true},
		{name: "rejected", verdict: policy.Deny("hold", "the review was rejected"), want: "403"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := newEchoUpstream(t, []byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"))
			pol := &decidingPolicy{started: make(chan struct{}, 1), verdict: make(chan policy.Verdict)}
			srv := startServer(t, proxy.Config{
				Upstream:   up.addr(),
				Protocol:   inspect.HTTP,
				Connection: "api",
				Policy:     pol,
				DenyWriter: proxy.ProtocolDenyWriter{},
			})

			c, err := net.Dial("tcp", srv.Addr().String())
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer c.Close()
			req := "POST /transfers HTTP/1.1\r\nHost: h\r\nContent-Length: 2\r\n\r\n{}"
			if _, err := c.Write([]byte(req)); err != nil {
				t.Fatalf("write: %v", err)
			}
			select {
			case <-pol.started:
			case <-time.After(3 * time.Second):
				t.Fatal("the request never reached the policy")
			}
			if got := up.got(); len(got) != 0 {
				t.Fatalf("upstream received %d bytes while the request was held", len(got))
			}
			pol.verdict <- tc.verdict

			if err := c.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatalf("set deadline: %v", err)
			}
			buf := make([]byte, 512)
			n, err := c.Read(buf)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if got := string(buf[:n]); !strings.Contains(got, tc.want) {
				t.Errorf("the client read %q, want %q", got, tc.want)
			}
			if forwarded := len(up.got()) > 0; forwarded != tc.forward {
				t.Errorf("upstream received the request: %v, want %v", forwarded, tc.forward)
			}
		})
	}
}

// ClickHouse native packets at revision 54450 (the codec's PinRevision),
// encoded once with github.com/ClickHouse/ch-go. Checked in as bytes so this
// module takes no second direct dependency.
const (
	chClientHelloHex = "000b746573742d636c69656e740101b2a90305617070646207617070757365720761707070617373"
	chServerHelloHex = "000a436c69636b486f7573651903b2a903000000"
	chDeleteQueryHex = "0103712d31010000000000000000000000010000000000b2a90300000000000002001f" +
		"44454c4554452046524f4d206f7264657273205748455245206964203d2037"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return b
}

// A ClickHouse query held for review reaches the upstream only once released,
// and a refusal reaches the client as a native ACCESS_DENIED exception.
func TestAClickHouseHoldForwardsOnlyWhatIsReleased(t *testing.T) {
	for _, tc := range []struct {
		name    string
		verdict policy.Verdict
		forward bool
	}{
		{name: "approved", verdict: policy.Verdict{}, forward: true},
		{name: "rejected", verdict: policy.Deny("hold", "the review was rejected")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hello, query := mustHex(t, chClientHelloHex), mustHex(t, chDeleteQueryHex)
			up := newEchoUpstream(t, mustHex(t, chServerHelloHex))
			pol := &decidingPolicy{started: make(chan struct{}, 1), verdict: make(chan policy.Verdict)}
			srv := startServer(t, proxy.Config{
				Upstream:   up.addr(),
				Protocol:   inspect.ClickHouse,
				Connection: "warehouse",
				Policy:     pol,
				DenyWriter: proxy.ProtocolDenyWriter{},
			})

			c, err := net.Dial("tcp", srv.Addr().String())
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer c.Close()
			if err := c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatalf("set deadline: %v", err)
			}
			if _, err := c.Write(hello); err != nil {
				t.Fatalf("write hello: %v", err)
			}
			if _, err := io.ReadFull(c, make([]byte, len(mustHex(t, chServerHelloHex)))); err != nil {
				t.Fatalf("read server hello: %v", err)
			}
			if _, err := c.Write(query); err != nil {
				t.Fatalf("write query: %v", err)
			}
			select {
			case <-pol.started:
			case <-time.After(3 * time.Second):
				t.Fatal("the query never reached the policy")
			}
			if got := up.got(); !bytes.Equal(got, hello) {
				t.Fatalf("upstream received %d bytes past the hello while the query was held", len(got)-len(hello))
			}
			pol.verdict <- tc.verdict

			if !tc.forward {
				got, _ := io.ReadAll(c)
				code, n := binary.Uvarint(got)
				if n <= 0 || code != 2 || len(got) < n+4 || binary.LittleEndian.Uint32(got[n:n+4]) != 497 {
					t.Errorf("the client read %x, want a ClickHouse ACCESS_DENIED exception", got)
				}
			}
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) && tc.forward && !bytes.Contains(up.got(), query) {
				time.Sleep(10 * time.Millisecond)
			}
			if forwarded := bytes.Contains(up.got(), query); forwarded != tc.forward {
				t.Errorf("upstream received the query: %v, want %v", forwarded, tc.forward)
			}
		})
	}
}

// The read-ahead hands chunks over in two alternating buffers. Traffic that
// spans many reads must arrive byte for byte, or the reader overwrote a chunk
// the pump was still forwarding.
func TestReadAheadRelaysEveryByte(t *testing.T) {
	up := newEchoUpstream(t, nil)
	srv := startServer(t, proxy.Config{
		Upstream:   up.addr(),
		Protocol:   inspect.Postgres,
		Connection: "appdb",
		Policy:     denyDrops(t),
	})

	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	go func() { _, _ = io.Copy(io.Discard, c) }()

	var want bytes.Buffer
	for i := range 400 {
		want.Write(pgQuery("SELECT " + strings.Repeat("x", i*7%900) + " FROM customers"))
	}
	if _, err := c.Write(want.Bytes()); err != nil {
		t.Fatalf("write: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for len(up.got()) < want.Len() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !bytes.Equal(up.got(), want.Bytes()) {
		t.Fatalf("upstream received %d bytes that differ from the %d sent", len(up.got()), want.Len())
	}
}

func waitForViolation(t *testing.T, sink *audit.MemorySink, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var found int
		for _, ev := range sink.Events() {
			if ev.Kind == audit.KindViolation {
				found++
				if !strings.Contains(ev.Message, want) {
					t.Errorf("the record says %q, want it to carry %q", ev.Message, want)
				}
			}
		}
		if found > 1 {
			t.Fatalf("the statement was recorded %d times, want once", found)
		}
		if found == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the held statement left no record")
}
