package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// cancelSink accepts one connection and records what it received.
func cancelSink(t *testing.T) (dial func(context.Context) (net.Conn, error), got func() []byte) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	received := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		b, _ := io.ReadAll(c)
		received <- b
	}()
	dial = func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", ln.Addr().String())
	}
	got = func() []byte {
		select {
		case b := <-received:
			return b
		case <-time.After(2 * time.Second):
			return nil
		}
	}
	return dial, got
}

func TestCancelRegistryRelaysUpstreamPair(t *testing.T) {
	dial, got := cancelSink(t)
	unregister := registerCancel(&CancelMapping{ClientPID: 10, ClientKey: 20, UpstreamPID: 30, UpstreamKey: 40}, dial)

	relayed, err := relayCancel(context.Background(), 10, 20)
	if err != nil || !relayed {
		t.Fatalf("relayCancel = %v, %v", relayed, err)
	}
	if want := pgCancelRequest(30, 40); !bytes.Equal(got(), want) {
		t.Fatalf("upstream received %x, want %x", got(), want)
	}

	unregister()
	if relayed, err := relayCancel(context.Background(), 10, 20); relayed || err != nil {
		t.Fatalf("after unregister relayCancel = %v, %v, want unknown", relayed, err)
	}
}

func TestNegotiateDownstreamCancelOnTerminatedLane(t *testing.T) {
	dial, got := cancelSink(t)
	defer registerCancel(&CancelMapping{ClientPID: 1, ClientKey: 2, UpstreamPID: 3, UpstreamKey: 4}, dial)()

	// Known pair: relayed to the recorded upstream, connection never a session.
	cl, srv := net.Pipe()
	go func(c net.Conn) { _, _ = c.Write(pgCancelRequest(1, 2)); c.Close() }(cl)
	_, _, err := negotiateDownstream(srv, inspect.Postgres, nil, time.Second, true)
	if !errors.Is(err, errCancelHandled) {
		t.Fatalf("known cancel: err = %v, want errCancelHandled", err)
	}
	if want := pgCancelRequest(3, 4); !bytes.Equal(got(), want) {
		t.Fatalf("upstream received %x, want %x", got(), want)
	}

	// Unknown pair on a terminated lane: dropped.
	cl, srv = net.Pipe()
	go func(c net.Conn) { _, _ = c.Write(pgCancelRequest(7, 7)); c.Close() }(cl)
	_, _, err = negotiateDownstream(srv, inspect.Postgres, nil, time.Second, true)
	if !errors.Is(err, errCancelHandled) {
		t.Fatalf("unknown cancel on a terminated lane: err = %v, want errCancelHandled", err)
	}

	// Unknown pair on a pass-through lane: handed through as before.
	cl, srv = net.Pipe()
	go func(c net.Conn) { _, _ = c.Write(pgCancelRequest(7, 7)) }(cl)
	conn, params, err := negotiateDownstream(srv, inspect.Postgres, nil, time.Second, false)
	if err != nil || params != nil {
		t.Fatalf("pass-through cancel: conn %v params %v err %v", conn, params, err)
	}
	buf := make([]byte, 16)
	if _, err := io.ReadFull(conn, buf); err != nil || !bytes.Equal(buf, pgCancelRequest(7, 7)) {
		t.Fatalf("pass-through cancel not left for the gate: %x %v", buf, err)
	}
	cl.Close()
}
