package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// These tests reproduce the "session terminated (404), need to re-initialize"
// failure that Devin/Cursor hit, and prove our fix resolves it — without a
// gateway, Postgres, or a real client.
//
// The trigger has two ingredients, both reproduced here:
//   - The MCP client does NOT keep a standalone GET SSE stream open
//     (StreamableClientTransport.DisableStandaloneSSE = true). Devin/Cursor
//     behave this way; the go-sdk's own client does not, which is why
//     `hoop admin mcp` never reproduced the bug.
//   - The server pings on a timer (ServerOptions.KeepAlive). With no standalone
//     stream to carry it, every ping fails and the SDK closes the whole
//     session, so the next tool call returns 404 ErrSessionMissing.

type pingArgs struct{}

// newTestMCP starts an in-memory streamable MCP server exposing a single
// "ping" tool, configured with the given server/handler options, and returns a
// connected client session. The client mimics Devin/Cursor by never opening a
// standalone SSE stream.
func newTestMCP(t *testing.T, serverOpts *mcp.ServerOptions, handlerOpts *mcp.StreamableHTTPOptions) *mcp.ClientSession {
	t.Helper()

	server := mcp.NewServer(&mcp.Implementation{Name: "hoop-test", Version: "test"}, serverOpts)
	mcp.AddTool(server, &mcp.Tool{Name: "ping", Description: "returns pong"},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ pingArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil, nil
		})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, handlerOpts)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	ctx := context.Background()
	transport := &mcp.StreamableClientTransport{
		Endpoint:             httpServer.URL,
		DisableStandaloneSSE: true, // <- the Devin/Cursor condition
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "devin-like", Version: "test"}, nil).
		Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callPing(ctx context.Context, s *mcp.ClientSession) error {
	_, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "ping"})
	return err
}

// TestKeepAlive_ReproducesSessionTermination demonstrates the ORIGINAL bug:
// with ServerOptions.KeepAlive set and a client that holds no standalone
// stream, the session is torn down between calls and the second call fails.
// This guards against anyone reintroducing KeepAlive.
func TestKeepAlive_ReproducesSessionTermination(t *testing.T) {
	ctx := context.Background()
	// Short keepalive so the test is fast; production used 30s.
	session := newTestMCP(t, &mcp.ServerOptions{KeepAlive: 100 * time.Millisecond}, nil)

	if err := callPing(ctx, session); err != nil {
		t.Fatalf("first call should succeed while the session is fresh, got: %v", err)
	}

	// Let at least one keepalive ping fire and fail (no standalone stream).
	time.Sleep(400 * time.Millisecond)

	err := callPing(ctx, session)
	if err == nil {
		t.Fatal("expected the second call to fail after the keepalive killed the session, but it succeeded")
	}
	if !errors.Is(err, mcp.ErrSessionMissing) {
		t.Errorf("expected ErrSessionMissing (the 404 surfaced to the client), got: %v", err)
	}
}

// TestProductionConfig_SurvivesIdleGap proves the FIX: with our production
// transport configuration (no KeepAlive; an inactivity SessionTimeout) the same
// Devin-like client can sit idle across what used to be the keepalive window
// and still call tools — no 404, no re-initialize.
func TestProductionConfig_SurvivesIdleGap(t *testing.T) {
	ctx := context.Background()
	// Mirror mcpserver.New: no ServerOptions.KeepAlive, inactivity SessionTimeout
	// on the handler. A generous timeout that will not fire during the test.
	session := newTestMCP(t, nil, &mcp.StreamableHTTPOptions{SessionTimeout: mcpSessionTimeout})

	if err := callPing(ctx, session); err != nil {
		t.Fatalf("first call failed: %v", err)
	}

	// Idle far longer than the old 100ms-1-tick failure window above.
	time.Sleep(500 * time.Millisecond)

	if err := callPing(ctx, session); err != nil {
		t.Fatalf("second call after idle gap should succeed with the fix, got: %v", err)
	}
}
