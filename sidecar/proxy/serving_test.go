package proxy_test

import (
	"context"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// Serving is what a health endpoint reads: false before the bind, true while
// the lane accepts, false once it closed. Addr cannot tell the last state: it
// keeps the bound address after Close.
func TestServingFollowsTheBindAndTheClose(t *testing.T) {
	srv, err := proxy.NewServer(proxy.Config{
		Listen:     "127.0.0.1:0",
		Upstream:   "127.0.0.1:1",
		Protocol:   inspect.Postgres,
		Connection: "appdb",
		Audit:      audit.NewMemorySink(8),
		Logger:     quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if srv.Serving() || srv.Addr() != nil {
		t.Fatal("a lane that never bound reports serving")
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(context.Background()) }()
	deadline := time.Now().Add(5 * time.Second)
	for !srv.Serving() {
		if time.Now().After(deadline) {
			t.Fatal("the lane never reported serving")
		}
		time.Sleep(time.Millisecond)
	}
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	if srv.Serving() {
		t.Error("a closed lane reports serving")
	}
	if srv.Addr() == nil {
		t.Error("Addr dropped the bound address; the health check relies on Serving, not on this")
	}
	if err := <-done; err != nil {
		t.Fatalf("Serve after Close = %v", err)
	}
	if srv.Serving() {
		t.Error("a lane whose Serve returned reports serving")
	}
}
