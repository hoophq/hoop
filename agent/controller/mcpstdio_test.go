package controller

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/agent/config"
	"github.com/hoophq/hoop/client/proxy"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	mcpbackend "github.com/hoophq/mcpproxy/backend"
)

// loopTransport stands in for the gateway between the agent and the CLI.
//
// The real gateway forwards agent packets to the client's stream by session id
// and client packets to the agent, touching neither payload nor spec
// (gateway/transport/agent.go, gateway/transport/client.go). This does the
// same thing in one process, so the test exercises the two real ends — the
// agent's tunnelled backend and the CLI's child owner — rather than mocks of
// them.
type loopTransport struct {
	mu       sync.Mutex
	toClient func(*pb.Packet)
	toAgent  func(*pb.Packet)
	sent     []*pb.Packet
}

func (t *loopTransport) Send(pkt *pb.Packet) error {
	t.mu.Lock()
	t.sent = append(t.sent, pkt)
	toClient, toAgent := t.toClient, t.toAgent
	t.mu.Unlock()

	switch pb.PacketType(pkt.Type) {
	case pbclient.MCPStdioRequest, pbclient.MCPStdioClose:
		if toClient != nil {
			go toClient(pkt)
		}
	case pbagent.MCPStdioReply:
		if toAgent != nil {
			go toAgent(pkt)
		}
	}
	return nil
}

func (t *loopTransport) Recv() (*pb.Packet, error)      { select {} }
func (t *loopTransport) StreamContext() context.Context { return context.Background() }
func (t *loopTransport) StartKeepAlive()                {}
func (t *loopTransport) Close() (error, error)          { return nil, nil }

func (t *loopTransport) packetsOfType(pktType string) []*pb.Packet {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []*pb.Packet
	for _, p := range t.sent {
		if p.Type == pktType {
			out = append(out, p)
		}
	}
	return out
}

// fakeMCPServerPath returns the stdio MCP server used as the "server on the
// user's laptop". It lives beside this test so the test is self-contained.
func fakeMCPServerPath(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required to run a stdio MCP server")
	}
	return filepath.Join("testdata", "fakemcp.js")
}

// newTunnelPair wires an agent backend to a real CLI-side child owner through
// the loop transport, and returns the backend ready to Send.
func newTunnelPair(t *testing.T, sessionID string, env map[string]string) (*clientStdioBackend, *loopTransport) {
	t.Helper()

	transport := &loopTransport{}
	agent := New(transport, &config.Config{}, nil)

	// The connection the admin configured: a client-hosted stdio MCP server.
	agent.connStore.Set(sessionID, &pb.AgentConnectionParams{
		ConnectionName: "laptop-mcp",
		ConnectionType: string(pb.ConnectionTypeMcpProxy),
		CmdList:        []string{"node", fakeMCPServerPath(t)},
		EnvVars:        mcpProxyEnvVars(mergeEnv(map[string]string{"MCP_TRANSPORT": mcpTransportClientStdio}, env)),
		UserID:         "user-1",
		UserEmail:      "dev@example.com",
	})

	stdio := proxy.NewMCPStdio(transport, sessionID)
	t.Cleanup(func() { _ = stdio.Close() })

	transport.mu.Lock()
	transport.toClient = func(pkt *pb.Packet) {
		switch pb.PacketType(pkt.Type) {
		case pbclient.MCPStdioRequest:
			stdio.PacketWriteClient(pkt)
		case pbclient.MCPStdioClose:
			stdio.PacketCloseClient(pkt)
		}
	}
	transport.toAgent = agent.processMCPStdioReply
	transport.mu.Unlock()

	factory := agent.clientStdioFactory("laptop-mcp", sessionID)
	b, err := factory(context.Background())
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	backend := b.(*clientStdioBackend)
	if err := backend.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	return backend, transport
}

func mergeEnv(base, extra map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// roundTrip sends one request and returns the envelope the server answered
// with, which arrives on Recv exactly as it would from a local child.
func roundTrip(t *testing.T, b *clientStdioBackend, msg string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := b.Send(ctx, []byte(msg)); err != nil {
		t.Fatalf("send %s: %v", msg, err)
	}
	select {
	case raw, ok := <-b.Recv():
		if !ok {
			t.Fatal("backend closed before replying")
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("reply is not json: %v (%s)", err, raw)
		}
		return out
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for the mcp server reply")
		return nil
	}
}

// A tool call must reach a process on the CONNECTING USER's machine and its
// result must come back through the tunnel. This is the whole point of the
// transport: without it the request would run as a child of the agent.
func TestClientStdioBackendRoundTrip(t *testing.T) {
	backend, transport := newTunnelPair(t, "sid-round-trip", nil)

	init := roundTrip(t, backend, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	result, _ := init["result"].(map[string]any)
	server, _ := result["serverInfo"].(map[string]any)
	if name, _ := server["name"].(string); name != "fake-mcp-on-laptop" {
		t.Fatalf("initialize served by %v, want the client-hosted server", server)
	}

	// A second request on the same backend must reuse the same child rather
	// than spawning a new one: an MCP server holds session state, and a fresh
	// process per call would lose it.
	call := roundTrip(t, backend, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami"}}`)
	callResult, _ := call["result"].(map[string]any)
	content, _ := callResult["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("tools/call returned no content: %v", call)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	if !strings.Contains(text, "ran on pid") {
		t.Fatalf("tool result = %q, want the local process report", text)
	}

	// Both requests must have travelled as MCPStdioRequest packets carrying
	// the command, which is what lets the CLI spawn without a handshake.
	requests := transport.packetsOfType(pbclient.MCPStdioRequest)
	if len(requests) != 2 {
		t.Fatalf("sent %d stdio requests, want 2", len(requests))
	}
	var cmd []string
	if err := pb.GobDecodeInto(requests[0].Spec[pb.SpecMCPStdioCommandKey], &cmd); err != nil {
		t.Fatalf("request carries no decodable command: %v", err)
	}
	if len(cmd) == 0 || cmd[0] != "node" {
		t.Fatalf("command = %v, want the configured node server", cmd)
	}
}

// The connection's MCPENV_* settings must reach the child's environment.
// They are how a client-hosted server receives its API tokens, so dropping
// them silently produces an unauthenticated server rather than an error.
func TestClientStdioBackendPassesEnvToChild(t *testing.T) {
	backend, _ := newTunnelPair(t, "sid-env", map[string]string{"MCPENV_WHOAMI": "hoop-user"})

	roundTrip(t, backend, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	call := roundTrip(t, backend, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami"}}`)

	result, _ := call["result"].(map[string]any)
	content, _ := result["content"].([]any)
	first, _ := content[0].(map[string]any)
	if text, _ := first["text"].(string); !strings.Contains(text, "as hoop-user") {
		t.Fatalf("tool result = %q, want the MCPENV_ value in the child env", text)
	}
}

// A notification carries no JSON-RPC id, so the server will never answer it.
// Send must still return as soon as the write is acknowledged; blocking on a
// response would wedge the gateway pipeline until the request timeout.
func TestClientStdioBackendNotificationDoesNotBlock(t *testing.T) {
	backend, _ := newTunnelPair(t, "sid-notify", nil)

	done := make(chan error, 1)
	go func() {
		done <- backend.Send(context.Background(),
			[]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("notification returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Send blocked on a notification that has no reply")
	}
}

// Closing the backend must reap the process on the user's machine. A leaked
// MCP server keeps running with the user's credentials long after the session
// it belonged to ended.
func TestClientStdioBackendCloseReapsChild(t *testing.T) {
	backend, transport := newTunnelPair(t, "sid-close", nil)
	roundTrip(t, backend, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)

	if err := backend.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Recv and Done must both be closed or the gateway's pump goroutine
	// blocks forever on a backend that is already gone.
	select {
	case _, ok := <-backend.Recv():
		if ok {
			t.Fatal("Recv still delivering after Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Recv was not closed by Close")
	}
	select {
	case <-backend.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done was not closed by Close")
	}

	// The CLI is told to reap, which is what actually kills the process.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(transport.packetsOfType(pbclient.MCPStdioClose)) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(transport.packetsOfType(pbclient.MCPStdioClose)) == 0 {
		t.Fatal("Close did not ask the client to stop the child")
	}

	// Sending afterwards must fail rather than hang.
	if err := backend.Send(context.Background(),
		[]byte(`{"jsonrpc":"2.0","id":9,"method":"tools/list"}`)); err != mcpbackend.ErrClosed {
		t.Fatalf("Send after Close = %v, want ErrClosed", err)
	}
}

// Close before Start must still release the channels. The gateway calls
// shutdown on every backend when any one of them fails to start, so a backend
// that never started still gets closed.
func TestClientStdioBackendCloseBeforeStart(t *testing.T) {
	transport := &loopTransport{}
	agent := New(transport, &config.Config{}, nil)
	agent.connStore.Set("sid-unstarted", &pb.AgentConnectionParams{
		ConnectionName: "laptop-mcp",
		ConnectionType: string(pb.ConnectionTypeMcpProxy),
		CmdList:        []string{"node", fakeMCPServerPath(t)},
		EnvVars:        mcpProxyEnvVars(map[string]string{"MCP_TRANSPORT": mcpTransportClientStdio}),
	})

	b, err := agent.clientStdioFactory("laptop-mcp", "sid-unstarted")(context.Background())
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case <-b.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done not closed when the backend never started")
	}
}

// A connection with no command cannot be served. Failing when the backend is
// built turns it into a clean error on initialize instead of a tool call that
// hangs until the 30-minute request timeout.
func TestClientStdioFactoryRequiresCommand(t *testing.T) {
	transport := &loopTransport{}
	agent := New(transport, &config.Config{}, nil)
	agent.connStore.Set("sid-nocmd", &pb.AgentConnectionParams{
		ConnectionName: "laptop-mcp",
		ConnectionType: string(pb.ConnectionTypeMcpProxy),
		EnvVars:        mcpProxyEnvVars(map[string]string{"MCP_TRANSPORT": mcpTransportClientStdio}),
	})

	if _, err := agent.clientStdioFactory("laptop-mcp", "sid-nocmd")(context.Background()); err == nil {
		t.Fatal("a client-stdio connection with no command was accepted")
	}
}

// A spawn failure on the user's machine must surface as an error on Send.
// Without the error packet the agent would wait out the full request timeout
// for a process that was never created.
func TestClientStdioBackendReportsSpawnFailure(t *testing.T) {
	transport := &loopTransport{}
	agent := New(transport, &config.Config{}, nil)
	agent.connStore.Set("sid-badcmd", &pb.AgentConnectionParams{
		ConnectionName: "laptop-mcp",
		ConnectionType: string(pb.ConnectionTypeMcpProxy),
		CmdList:        []string{filepath.Join(os.TempDir(), "hoop-no-such-mcp-server")},
		EnvVars:        mcpProxyEnvVars(map[string]string{"MCP_TRANSPORT": mcpTransportClientStdio}),
	})

	stdio := proxy.NewMCPStdio(transport, "sid-badcmd")
	t.Cleanup(func() { _ = stdio.Close() })
	transport.mu.Lock()
	transport.toClient = func(pkt *pb.Packet) { stdio.PacketWriteClient(pkt) }
	transport.toAgent = agent.processMCPStdioReply
	transport.mu.Unlock()

	b, err := agent.clientStdioFactory("laptop-mcp", "sid-badcmd")(context.Background())
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	backend := b.(*clientStdioBackend)
	if err := backend.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = backend.Send(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err == nil {
		t.Fatal("Send succeeded against a command that cannot be spawned")
	}
	if !strings.Contains(err.Error(), "mcp stdio backend on client") {
		t.Fatalf("error = %v, want the failure reported from the client", err)
	}
}
