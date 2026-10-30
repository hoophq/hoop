package mcpgateway

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/mcpproxy/audit"
	"github.com/hoophq/mcpproxy/inspect"
	"github.com/hoophq/mcpproxy/session"
)

type nopSession struct{}

func (nopSession) ID() string { return "sidecar" }
func (nopSession) ToolCall(context.Context, string, []byte) daemon.MCPVerdict {
	return daemon.MCPVerdict{Allowed: true}
}
func (nopSession) ToolList(context.Context, []daemon.MCPToolInfo) daemon.MCPVerdict {
	return daemon.MCPVerdict{Allowed: true}
}
func (nopSession) Record(context.Context, string, map[string]string) {}
func (nopSession) Close(context.Context)                             {}

// A message that reaches the pipeline after its MCP session ended, after a
// reload ended it, or after the lane closed opens no sidecar session.
func TestLateMessagesOpenNoSidecarSession(t *testing.T) {
	var opened atomic.Int32
	built, err := Build(daemon.MCPGatewayLane{
		Name: "u", Listen: "127.0.0.1:0", Upstream: "http://127.0.0.1:1/mcp",
		Open: func(context.Context, daemon.MCPCaller) (daemon.MCPLaneSession, error) {
			opened.Add(1)
			return nopSession{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := built.(*server)
	ctx := context.Background()
	open := func(id string) error {
		_, err := s.laneSession(ctx, session.New(id, inspect.Identity{}))
		return err
	}

	if err := open("ended"); err != nil {
		t.Fatal(err)
	}
	s.Emit(ctx, audit.Event{Type: audit.EventSessionEnd, Session: "ended"})
	if err := open("ended"); !errors.Is(err, errSessionEnded) {
		t.Fatalf("a message after the session ended: %v", err)
	}

	if err := open("reloaded"); err != nil {
		t.Fatal(err)
	}
	s.EndSessions("rules reloaded")
	s.Emit(ctx, audit.Event{Type: audit.EventSessionEnd, Session: "reloaded"})
	if err := open("reloaded"); !errors.Is(err, errSessionEnded) {
		t.Fatalf("a message after a reload and the end it caused: %v", err)
	}

	_ = s.Close()
	if err := open("late"); !errors.Is(err, errServerClosed) {
		t.Fatalf("a message after Close: %v", err)
	}
	if n := opened.Load(); n != 2 {
		t.Fatalf("opened %d sidecar sessions, want 2", n)
	}
}
