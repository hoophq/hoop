package proxy_test

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/proxy"
)

type slowModel struct{ delay time.Duration }

func (slowModel) Name() string { return "stub" }

func (m slowModel) Classify(ctx context.Context, _, _ string) (*analyzer.Result, error) {
	select {
	case <-time.After(m.delay):
		return &analyzer.Result{RiskLevel: analyzer.RiskLow}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// headerTimeoutUpstream answers 200 "ok" and closes a connection that sends
// no request head within a second, as go-httpbin does.
func headerTimeoutUpstream(t *testing.T) (addr string, conns *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("upstream listen: %v", err)
	}
	conns = new(atomic.Int32)
	srv := &http.Server{
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "ok")
		}),
		ConnState: func(_ net.Conn, st http.ConnState) {
			if st == http.StateNew {
				conns.Add(1)
			}
		},
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String(), conns
}

// getThrough sends one GET through srv and returns the response, its body
// read, and the reader for what follows it.
func getThrough(t *testing.T, srv *proxy.Server) (*http.Response, *bufio.Reader) {
	t.Helper()
	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(c, "GET /get HTTP/1.1\r\nHost: api\r\n\r\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v; the client got no answer", err)
	}
	if body, err := io.ReadAll(resp.Body); err != nil || string(body) != "ok" {
		t.Fatalf("body = %q, %v", body, err)
	}
	return resp, br
}

func TestHTTPSlowVerdictOutlivesTheUpstreamHeaderTimeout(t *testing.T) {
	addr, conns := headerTimeoutUpstream(t)
	ev, err := analyzer.New(analyzer.Config{
		Rule:     "api",
		Provider: slowModel{delay: 1500 * time.Millisecond},
		Trigger:  analyzer.Trigger{All: true},
	})
	if err != nil {
		t.Fatalf("analyzer.New: %v", err)
	}
	srv := startServer(t, proxy.Config{Upstream: addr, Protocol: inspect.HTTP, Connection: "api", Policy: ev})

	resp, _ := getThrough(t, srv)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if n := conns.Load(); n != 2 {
		t.Errorf("upstream saw %d connections, want 2: the idle one it closed and the one that carried the request", n)
	}
}

func TestHTTPRequestUsesTheConnectionDialedAtAccept(t *testing.T) {
	addr, conns := headerTimeoutUpstream(t)
	srv := startServer(t, proxy.Config{Upstream: addr, Protocol: inspect.HTTP, Connection: "api"})

	resp, _ := getThrough(t, srv)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if n := conns.Load(); n != 1 {
		t.Errorf("upstream saw %d connections, want 1", n)
	}
}

// Only a connection that never carried a byte is dialed again: one the
// upstream closes after it answered ends the client's connection.
func TestHTTPUpstreamCloseAfterARequestEndsTheConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("upstream listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	var conns atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns.Add(1)
			go func() {
				defer c.Close()
				if _, err := http.ReadRequest(bufio.NewReader(c)); err != nil {
					return
				}
				_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
			}()
		}
	}()
	srv := startServer(t, proxy.Config{Upstream: ln.Addr().String(), Protocol: inspect.HTTP, Connection: "api"})

	_, br := getThrough(t, srv)
	if _, err := br.ReadByte(); err != io.EOF {
		t.Fatalf("read after the response = %v, want EOF", err)
	}
	if n := conns.Load(); n != 1 {
		t.Errorf("upstream saw %d connections, want 1", n)
	}
}
