package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// endsAt is a check response that is done at the first "Z".
type endsAt struct{ got []byte }

func (r *endsAt) Feed(data []byte) ([]byte, int, bool, error) {
	if i := bytes.IndexByte(data, 'Z'); i >= 0 {
		r.got = append(r.got, data[:i+1]...)
		return nil, i + 1, true, nil
	}
	r.got = append(r.got, data...)
	return nil, len(data), false, nil
}

func (r *endsAt) Result() inspect.CheckResult { return inspect.CheckResult{} }

type sideFixture struct {
	path     *sidePath
	server   net.Conn
	accepted chan net.Conn
	ended    chan error
	client   net.Conn
}

func newSideFixture(t *testing.T) *sideFixture {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	accepted := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server := <-accepted
	t.Cleanup(func() { client.Close(); server.Close() })

	ctx, end := context.WithCancelCause(context.Background())
	ended := make(chan error, 1)
	s := &Server{cfg: Config{Upstream: ln.Addr().String(), DialTimeout: time.Second}}
	f := &sideFixture{server: server, accepted: accepted, ended: ended, client: client}
	f.path = newSidePath(s, client, ctx, func(err error) {
		select {
		case ended <- err:
		default:
		}
		end(err)
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return f
}

// pump plays the server pump: it reads the upstream and gives every chunk to
// the side path, returning what the client would get.
func (f *sideFixture) pump() <-chan []byte {
	out := make(chan []byte, 1)
	go func() {
		var toClient []byte
		buf := make([]byte, 512)
		for {
			f.path.enterRead()
			n, err := f.client.Read(buf)
			f.path.leaveRead()
			if err != nil {
				f.path.abort(err)
				out <- toClient
				return
			}
			fwd, rest, err := f.path.divert(buf[:n])
			toClient = append(append(toClient, fwd...), rest...)
			if err != nil {
				out <- toClient
				return
			}
		}
	}()
	return out
}

func readExactly(t *testing.T, c net.Conn, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(c, b); err != nil {
		t.Fatalf("read %d bytes: %v", n, err)
	}
	return b
}

func TestACheckAnswerIsNotSentToTheClient(t *testing.T) {
	f := newSideFixture(t)
	toClient := f.pump()

	resp := &endsAt{}
	done := make(chan error, 1)
	go func() { done <- f.path.Exchange(context.Background(), []byte("check"), resp, []byte("cancel")) }()

	if got := readExactly(t, f.server, 5); string(got) != "check" {
		t.Fatalf("upstream got %q", got)
	}
	if _, err := f.server.Write([]byte("answerZafter")); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if string(resp.got) != "answerZ" {
		t.Fatalf("the check read %q", resp.got)
	}
	f.client.Close()
	if got := <-toClient; string(got) != "after" {
		t.Fatalf("the client got %q, want only what followed the check", got)
	}
}

func TestATimedOutCheckIsCancelledOnANewConnectionAndDrained(t *testing.T) {
	f := newSideFixture(t)
	f.pump()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	resp := &endsAt{}
	done := make(chan error, 1)
	go func() { done <- f.path.Exchange(ctx, []byte("check"), resp, []byte("cancel")) }()
	readExactly(t, f.server, 5)

	var cancelConn net.Conn
	select {
	case cancelConn = <-f.accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("no cancel connection")
	}
	if got := readExactly(t, cancelConn, 6); string(got) != "cancel" {
		t.Fatalf("the cancel connection got %q", got)
	}
	select {
	case err := <-done:
		t.Fatalf("the check ended before the server closed the cancel connection: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cancelConn.Close()
	if _, err := f.server.Write([]byte("canceledZ")); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, policy.ErrCheckCancelled) {
		t.Fatalf("a drained check returned %v, want ErrCheckCancelled", err)
	}
	select {
	case err := <-f.ended:
		t.Fatalf("a drained check ended the connection: %v", err)
	default:
	}
}

func TestACheckThatDoesNotDrainClosesTheConnection(t *testing.T) {
	f := newSideFixture(t)
	f.pump()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.path.Exchange(ctx, []byte("check"), &endsAt{}, []byte("cancel")) }()
	readExactly(t, f.server, 5)
	go func() {
		c := <-f.accepted
		_, _ = io.Copy(io.Discard, c)
	}()

	select {
	case err := <-done:
		if !errors.Is(err, errCheckNotDrained) {
			t.Fatalf("got %v, want errCheckNotDrained", err)
		}
	case <-time.After(checkDrainTimeout + 2*time.Second):
		t.Fatal("the check never gave up")
	}
	if err := <-f.ended; !errors.Is(err, errCheckNotDrained) {
		t.Fatalf("the connection ended with %v", err)
	}
	_ = f.server.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := f.server.Read(make([]byte, 1)); err == nil {
		t.Fatal("the upstream connection is still open")
	}
}

func TestACheckWaitsForNoClientWrite(t *testing.T) {
	f := newSideFixture(t)
	start := time.Now()
	err := f.path.Exchange(context.Background(), []byte("check"), &endsAt{}, nil)
	if !errors.Is(err, policy.ErrCheckNotReady) {
		t.Fatalf("got %v, want ErrCheckNotReady", err)
	}
	if took := time.Since(start); took > readWait+time.Second {
		t.Fatalf("the refusal took %s", took)
	}
	_ = f.server.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if n, _ := f.server.Read(make([]byte, 8)); n > 0 {
		t.Fatal("a refused check reached the upstream")
	}
}
