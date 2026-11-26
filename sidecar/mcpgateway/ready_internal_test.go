package mcpgateway

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/daemon"
)

func buildReadyLane(t *testing.T, listen string) *server {
	t.Helper()
	built, err := Build(daemon.MCPGatewayLane{
		Name: "tools", Listen: listen, Upstream: "http://127.0.0.1:1/mcp",
		Open: func(context.Context, daemon.MCPCaller) (daemon.MCPLaneSession, error) {
			return nopSession{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return built.(*server)
}

// ENG-1116: /readyz judges the MCP lane by Ready, which covers the bind and
// the close. Addr keeps the bound address after Close, so it cannot say the
// lane still serves.
func TestReadyFollowsTheBindAndTheClose(t *testing.T) {
	s := buildReadyLane(t, "127.0.0.1:0")
	if s.Ready() || s.Addr() != nil {
		t.Fatal("a lane that never bound is ready or has an address")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for !s.Ready() {
		if time.Now().After(deadline) {
			t.Fatal("the lane never became ready")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve = %v", err)
	}
	if s.Ready() {
		t.Error("a closed lane is ready")
	}
	if s.Addr() == nil {
		t.Error("Addr dropped the bound address after Close; Ready is what tells the close")
	}
}

// A lane whose bind fails is never ready, also in the instant before Serve
// returns the error that stops the process.
func TestReadyIsFalseWhenTheBindFails(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	s := buildReadyLane(t, held.Addr().String())
	if err := s.Serve(context.Background()); err == nil {
		t.Fatal("Serve bound a held port")
	}
	if s.Ready() {
		t.Error("a lane that failed to bind is ready")
	}
	_ = s.Close()
}
