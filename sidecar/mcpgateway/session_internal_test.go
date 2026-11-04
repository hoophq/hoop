package mcpgateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/mcpproxy/audit"
	"github.com/hoophq/mcpproxy/inspect"
	"github.com/hoophq/mcpproxy/jsonrpc"
	mcpwire "github.com/hoophq/mcpproxy/mcp"
	"github.com/hoophq/mcpproxy/session"
)

type nopSession struct{}

func (nopSession) ID() string { return "sidecar" }
func (nopSession) ToolCall(context.Context, string, []byte) daemon.MCPVerdict {
	return daemon.MCPVerdict{Allowed: true}
}
func (nopSession) Hidden(string) bool { return false }
func (nopSession) ToolList(context.Context, []daemon.MCPToolInfo, []string) daemon.MCPVerdict {
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

type recordingSession struct {
	nopSession
	acts []string
}

func (r *recordingSession) Hidden(tool string) bool { return tool == "delete_customer" }
func (r *recordingSession) Record(_ context.Context, activity string, attrs map[string]string) {
	r.acts = append(r.acts, activity+": "+attrs["reason"])
}

// A tools/list result the lane cannot read ends the session: it could neither
// hide a tool from it nor judge it, so it never reaches the client as sent.
func TestAnUnreadableCatalogEndsTheSession(t *testing.T) {
	rec := &recordingSession{}
	built, err := Build(daemon.MCPGatewayLane{
		Name: "u", Listen: "127.0.0.1:0", Upstream: "http://127.0.0.1:1/mcp",
		Open: func(context.Context, daemon.MCPCaller) (daemon.MCPLaneSession, error) { return rec, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	s := built.(*server)
	for _, result := range []string{
		`{"tools":[{"name":"delete_customer"},{"name":7}]}`,
		`{"tools":"none"}`,
	} {
		rec.acts = nil
		m := &inspect.Msg{Dir: inspect.S2C, CorrelatedMethod: mcpwire.MethodToolsList,
			Env: &jsonrpc.Message{ID: jsonrpc.ID{Raw: json.RawMessage("2")}, Result: json.RawMessage(result)}}
		d := (&hideCheck{s: s}).Inspect(context.Background(), session.New("m1", inspect.Identity{}), m)
		if d.Verdict != inspect.Kill || string(m.Env.Result) != result {
			t.Errorf("%s: verdict %v, result %s", result, d.Verdict, m.Env.Result)
		}
		if len(rec.acts) != 1 || !strings.HasPrefix(rec.acts[0], "mcp_catalog_unreadable") {
			t.Errorf("%s: activities %v", result, rec.acts)
		}
	}
}

type closeCounter struct {
	nopSession
	closed *atomic.Int32
}

func (c closeCounter) Close(context.Context) { c.closed.Add(1) }

// A session opens outside the server lock: a slow open stalls neither the
// open sessions nor a second open of the same session, which waits for the
// first and gets the same sidecar session. A reload during an open ends it.
func TestASlowSessionOpenStallsNothingElse(t *testing.T) {
	release := make(chan struct{})
	var opened, closed atomic.Int32
	built, err := Build(daemon.MCPGatewayLane{
		Name: "u", Listen: "127.0.0.1:0", Upstream: "http://127.0.0.1:1/mcp",
		Open: func(_ context.Context, c daemon.MCPCaller) (daemon.MCPLaneSession, error) {
			opened.Add(1)
			if c.MCPSessionID == "slow" {
				<-release
			}
			return closeCounter{closed: &closed}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := built.(*server)
	ctx := context.Background()
	if _, err := s.laneSession(ctx, session.New("fast", inspect.Identity{})); err != nil {
		t.Fatal(err)
	}

	slow := session.New("slow", inspect.Identity{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() { defer wg.Done(); _, errs[i] = s.laneSession(ctx, slow) }()
	}
	for {
		s.mu.Lock()
		n := len(s.opening)
		s.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	got := make(chan error, 1)
	go func() { _, err := s.laneSession(ctx, session.New("fast", inspect.Identity{})); got <- err }()
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("an open session waited on another session's open")
	}

	s.EndSessions("rules reloaded")
	close(release)
	wg.Wait()
	for i, err := range errs {
		if !errors.Is(err, errSessionEnded) {
			t.Errorf("open %d of the slow session: %v, want errSessionEnded", i, err)
		}
	}
	if n := opened.Load(); n != 2 {
		t.Errorf("opened %d sidecar sessions, want 2 (fast, slow once)", n)
	}
	if n := closed.Load(); n != 2 {
		t.Errorf("closed %d sidecar sessions, want 2 (fast by the reload, slow after its open)", n)
	}
}
