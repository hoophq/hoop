package proxy_test

import (
	"bytes"
	"context"
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
