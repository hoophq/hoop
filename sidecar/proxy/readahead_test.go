package proxy

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// scriptedConn replays one Read result. Only the read half is used: readAhead
// reads, sets deadlines and nothing else.
type scriptedConn struct {
	data []byte
	err  error
	done bool
}

func (c *scriptedConn) Read(b []byte) (int, error) {
	if c.done {
		return 0, io.EOF
	}
	c.done = true
	return copy(b, c.data), c.err
}

func (c *scriptedConn) Write(b []byte) (int, error) { return len(b), nil }
func (c *scriptedConn) Close() error                { return nil }
func (c *scriptedConn) LocalAddr() net.Addr         { return nil }
func (c *scriptedConn) RemoteAddr() net.Addr        { return nil }
func (c *scriptedConn) SetDeadline(time.Time) error { return nil }
func (c *scriptedConn) SetReadDeadline(t time.Time) error {
	return nil
}
func (c *scriptedConn) SetWriteDeadline(time.Time) error { return nil }

// A client may half-close its write side and hand over a complete final
// request in the same read. Those bytes are a statement like any other: the
// pump ends the connection after judging them, so a hold files its review
// rather than denying a request nobody withdrew.
func TestAFinalRequestIsDeliveredBeforeTheConnectionEnds(t *testing.T) {
	src := &scriptedConn{data: []byte("DELETE FROM users"), err: io.EOF}
	ended := make(chan error, 1)
	next, stop := readAhead(src, 0, func(cause error) { ended <- cause })
	defer stop()

	chunk, err := next()
	if string(chunk) != "DELETE FROM users" {
		t.Errorf("the pump received %q, want the final request", chunk)
	}
	if !errors.Is(err, io.EOF) {
		t.Errorf("the read error was %v, want io.EOF beside the bytes", err)
	}
	select {
	case cause := <-ended:
		t.Fatalf("the connection ended before its last request was judged: %v", cause)
	default:
	}
}

// A read with nothing to hand over is the hangup the pump cannot see for
// itself: it is blocked in the gate, and only this ends the wait.
func TestAHangupWithNoBytesEndsTheConnectionAtOnce(t *testing.T) {
	src := &scriptedConn{err: io.EOF}
	ended := make(chan error, 1)
	next, stop := readAhead(src, 0, func(cause error) { ended <- cause })
	defer stop()

	if _, err := next(); !errors.Is(err, io.EOF) {
		t.Fatalf("the read error was %v, want io.EOF", err)
	}
	select {
	case cause := <-ended:
		if cause == nil || cause.Error() != "the client closed the connection" {
			t.Errorf("the connection ended with %v, want the client hangup", cause)
		}
	default:
		t.Fatal("a hangup did not end the connection, so a hold would run to its budget")
	}
}
