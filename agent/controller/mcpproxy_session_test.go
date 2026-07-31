package controller

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	mcpbackend "github.com/hoophq/mcpproxy/backend"
	"github.com/hoophq/mcpproxy/checks"
	mcpconfig "github.com/hoophq/mcpproxy/config"
	mcpgateway "github.com/hoophq/mcpproxy/gateway"
	"github.com/hoophq/mcpproxy/inspect"
)

// mcpPost drives one JSON-RPC message through a gateway's HTTP handler the
// way hoop's proxy listener does: a POST to /mcp carrying the session header
// once the handshake assigned one.
//
// No MCP-Protocol-Version header is sent. That header marks the "Modern"
// stateless era, which the gateway refuses because its policy controls need
// session state (mcpproxy gateway/revision.go). A legacy client sends none,
// and a legacy client is what hoop proxies.
func mcpPost(t *testing.T, h http.Handler, sid, body string) (int, string, string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	if sid != "" {
		r.Header.Set("Mcp-Session-Id", sid)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	resp := w.Result()
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Mcp-Session-Id"), strings.TrimSpace(string(payload))
}

const (
	mcpInitialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"protocolVersion":"2025-11-25","capabilities":{},` +
		`"clientInfo":{"name":"hoop-test","version":"1"}}}`
	mcpToolsList = `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
)

// newTunnelGateway builds a real mcpproxy gateway whose single backend is the
// tunnelled one, so the whole path — HTTP in, inspection pipeline, tunnel out
// to a child on the "user's machine" and back — runs for real.
func newTunnelGateway(t *testing.T, sessionID string, policy mcpconfig.Policy) *mcpgateway.Gateway {
	t.Helper()
	backend, _ := newTunnelPair(t, sessionID, nil)

	gw, err := mcpgateway.New(mcpgateway.Options{
		Backends: map[string]mcpbackend.Factory{
			"laptop-mcp": func(context.Context) (mcpbackend.Backend, error) { return backend, nil },
		},
		Pipeline: checks.Assemble(policy, inspect.Hooks{}, nil, false),
		Resolver: func(*http.Request) (inspect.Identity, error) {
			return inspect.Identity{Subject: "user-1"}, nil
		},
	})
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	t.Cleanup(gw.Close)
	return gw
}

// An MCP client's second request must succeed.
//
// This is the regression the per-connection-id gateway had: the hoop proxy
// listener mints a fresh connection id for every inbound HTTP request, so
// keying the mcpproxy gateway on it produced a brand-new gateway for message
// two — one that had never issued the session id the client was presenting.
// initialize worked and everything after it returned "unknown session", while
// each request leaked another gateway and another MCP server process.
func TestMCPSessionSurvivesAcrossRequests(t *testing.T) {
	gw := newTunnelGateway(t, "sid-affinity", mcpconfig.Policy{})
	h := gw.Handler()

	code, sid, body := mcpPost(t, h, "", mcpInitialize)
	if code != http.StatusOK {
		t.Fatalf("initialize = HTTP %d: %s", code, body)
	}
	if sid == "" {
		t.Fatal("initialize returned no Mcp-Session-Id")
	}

	code, _, body = mcpPost(t, h, sid, mcpToolsList)
	if code != http.StatusOK {
		t.Fatalf("tools/list = HTTP %d: %s (the MCP session did not survive the request)", code, body)
	}
	if !strings.Contains(body, "whoami") {
		t.Fatalf("tools/list body = %s, want the client-hosted server's catalog", body)
	}
}

// Tool policy must apply to a client-hosted server exactly as it does to one
// running on the agent. This is the reason for tunnelling rather than letting
// the MCP client talk to its local server directly: the inspection pipeline
// still sees every call.
func TestMCPPolicyAppliesToClientHostedServer(t *testing.T) {
	gw := newTunnelGateway(t, "sid-policy", mcpconfig.Policy{
		DeniedTools: []string{"delete_*"},
	})
	h := gw.Handler()

	_, sid, _ := mcpPost(t, h, "", mcpInitialize)

	// A denied tool is filtered out of the catalog before the model sees it.
	_, _, list := mcpPost(t, h, sid, mcpToolsList)
	if strings.Contains(list, "delete_everything") {
		t.Fatalf("denied tool advertised in tools/list: %s", list)
	}
	if !strings.Contains(list, "whoami") {
		t.Fatalf("allowed tool missing from tools/list: %s", list)
	}

	// And calling it anyway is refused rather than forwarded to the laptop.
	_, _, call := mcpPost(t, h, sid,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"delete_everything","arguments":{}}}`)
	var envelope map[string]any
	if err := json.Unmarshal([]byte(call), &envelope); err != nil {
		t.Fatalf("tools/call reply is not json: %s", call)
	}
	if _, denied := envelope["error"]; !denied {
		t.Fatalf("denied tool executed on the user's machine: %s", call)
	}
}

// The tunnel must carry a real tool result back, not just handshake traffic.
func TestMCPToolCallReachesClientHostedServer(t *testing.T) {
	gw := newTunnelGateway(t, "sid-call", mcpconfig.Policy{})
	h := gw.Handler()

	_, sid, _ := mcpPost(t, h, "", mcpInitialize)
	mcpPost(t, h, sid, mcpToolsList)

	code, _, body := mcpPost(t, h, sid,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"whoami","arguments":{}}}`)
	if code != http.StatusOK {
		t.Fatalf("tools/call = HTTP %d: %s", code, body)
	}
	if !strings.Contains(body, "ran on pid") {
		t.Fatalf("tools/call body = %s, want the local process report", body)
	}
}

// Session cleanup must close the gateway and reap the user's process. The
// gateway outlives individual requests by design, so nothing else would.
func TestSessionCleanupClosesMCPGateway(t *testing.T) {
	sessionID := "sid-cleanup"
	backend, transport := newTunnelPair(t, sessionID, nil)
	agent := backend.agent

	gw, err := mcpgateway.New(mcpgateway.Options{
		Backends: map[string]mcpbackend.Factory{
			"laptop-mcp": func(context.Context) (mcpbackend.Backend, error) { return backend, nil },
		},
		Pipeline: checks.Assemble(mcpconfig.Policy{}, inspect.Hooks{}, nil, false),
	})
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	agent.mcpGateways.Store(sessionID, &mcpGatewayHolder{gw: gw})

	if _, sid, _ := mcpPost(t, gw.Handler(), "", mcpInitialize); sid == "" {
		t.Fatal("initialize did not open a session")
	}

	agent.closeMCPProxyConnections(sessionID)

	if _, ok := agent.mcpGateways.Load(sessionID); ok {
		t.Fatal("gateway still registered after cleanup")
	}
	select {
	case <-backend.Done():
	default:
		t.Fatal("backend still live after session cleanup")
	}
	if len(transport.packetsOfType(pbclient.MCPStdioClose)) == 0 {
		t.Fatal("cleanup did not ask the client to stop the mcp server")
	}
}

// Two hoop sessions must not share one MCP server process: they are different
// users, or the same user twice, and a shared child would leak state between
// them.
func TestClientStdioBackendsAreScopedPerSession(t *testing.T) {
	a, _ := newTunnelPair(t, "sid-a", nil)
	b, _ := newTunnelPair(t, "sid-b", nil)

	if a.key() == b.key() {
		t.Fatalf("both sessions resolve to the same backend key %q", a.key())
	}
	roundTrip(t, a, mcpInitialize)
	roundTrip(t, b, mcpInitialize)

	// Closing one must not disturb the other.
	if err := a.Close(); err != nil {
		t.Fatalf("close a: %v", err)
	}
	reply := roundTrip(t, b, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"whoami"}}`)
	if _, ok := reply["result"]; !ok {
		t.Fatalf("second session broke when the first closed: %v", reply)
	}
}

// mcpGatewayFor must return the SAME gateway for every connection id in a
// session. This is the hoop-layer half of the affinity fix: the proxy
// listener hands the agent a new connection id per HTTP request, and building
// a gateway per id is what broke every message after initialize.
func TestMCPGatewayIsMemoisedPerSession(t *testing.T) {
	sessionID := "sid-memo"
	backend, _ := newTunnelPair(t, sessionID, nil)
	agent := backend.agent
	t.Cleanup(func() { agent.closeMCPProxyConnections(sessionID) })

	connParams := agent.connectionParams(sessionID)
	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionTypeMcpProxy)
	if err != nil {
		t.Fatalf("env: %v", err)
	}

	// Two different connection ids, as two consecutive HTTP requests produce.
	first, err := agent.mcpGatewayFor(sessionID, connenv, connParams,
		mcpProxyOpts(connenv, connParams, sessionID, "1"), nil)
	if err != nil {
		t.Fatalf("first gateway: %v", err)
	}
	second, err := agent.mcpGatewayFor(sessionID, connenv, connParams,
		mcpProxyOpts(connenv, connParams, sessionID, "2"), nil)
	if err != nil {
		t.Fatalf("second gateway: %v", err)
	}
	if first != second {
		t.Fatal("a second connection id built a second gateway; MCP sessions cannot survive that")
	}
}
