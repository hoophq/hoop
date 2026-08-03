package controller

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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

// A failed build must evict only its OWN holder.
//
// Several requests can be in mcpGatewayFor at once. They share one holder —
// that is what sync.Once is for — so when the build fails they ALL reach the
// eviction, one after another as each wakes from once.Do. A request arriving
// after the first of those evictions finds nothing memoised, stores a fresh
// holder and builds a healthy gateway behind it. The stragglers then evict
// that healthy successor if they delete by key rather than by identity, and
// nothing points at the live gateway any more: session cleanup finds no
// holder, never closes it, and its sink goroutine and stdio child outlive the
// session with no way to reach them.
//
// The successor is installed the moment the first eviction is observed, which
// is precisely when the stragglers are still in flight.
func TestFailedMCPGatewayBuildDoesNotEvictAHealthySuccessor(t *testing.T) {
	sessionID := "sid-evict"
	backend, _ := newTunnelPair(t, sessionID, nil)
	agent := backend.agent

	connParams := agent.connectionParams(sessionID)
	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionTypeMcpProxy)
	if err != nil {
		t.Fatalf("env: %v", err)
	}
	// Guardrail rules that cannot be materialized: libhoop refuses to build
	// the hooks, so every caller's shared build fails deterministically.
	opts := mcpProxyOpts(connenv, connParams, sessionID, "1")
	opts["guard_rail_rules"] = `{"input_rules":[{"type":"deny_words_list"}]}`

	// The successor: a live gateway with a live sink, exactly what a later
	// request would memoise once the failed holder is gone.
	gw, err := mcpgateway.New(mcpgateway.Options{
		Backends: map[string]mcpbackend.Factory{
			"laptop-mcp": func(context.Context) (mcpbackend.Backend, error) { return backend, nil },
		},
		Pipeline: checks.Assemble(mcpconfig.Policy{}, inspect.Hooks{}, nil, false),
	})
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	sink := agent.mcpAuditSink(sessionID, nil)
	t.Cleanup(sink.stop)
	winner := &mcpGatewayHolder{gw: gw, sink: sink}
	winner.once.Do(func() {})

	// Install the successor the instant the first straggler's eviction lands,
	// while the others are still on their way to theirs.
	installed := make(chan struct{})
	go func() {
		defer close(installed)
		for {
			if _, ok := agent.mcpGateways.Load(sessionID); !ok {
				agent.mcpGateways.Store(sessionID, winner)
				return
			}
		}
	}()

	// Enough concurrent callers that some are still parked in once.Do when
	// the first of them evicts.
	//
	// Individual return values are deliberately not asserted: a caller that
	// arrives after the successor is installed picks it up and legitimately
	// succeeds. What must hold is the state of the map afterwards, which is
	// what the stragglers can corrupt.
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = agent.mcpGatewayFor(sessionID, connenv, connParams, opts, nil)
		}()
	}
	wg.Wait()
	<-installed

	obj, ok := agent.mcpGateways.Load(sessionID)
	if !ok {
		t.Fatal("a failed build evicted the healthy successor; its gateway, sink goroutine and stdio child are now unreachable and session cleanup will never close them")
	}
	if obj.(*mcpGatewayHolder) != winner {
		t.Fatal("the memoised holder is not the successor")
	}

	// And cleanup can still reach it, which is the whole point of not
	// evicting it.
	agent.closeMCPProxyConnections(sessionID)
	waitSinkStopped(t, sink)
	if _, ok := agent.mcpGateways.Load(sessionID); ok {
		t.Fatal("cleanup did not drop the holder")
	}
}

// A gateway finished after the session closed must be torn down by its
// builder, not stranded.
//
// SessionClose stores closed=true and then looks for a holder. A build still
// running at that instant has not stored one yet, so cleanup tears down
// nothing; when the build finishes it would memoise a live gateway under a
// dead session — sink goroutine running until the agent exits, and on the
// client-stdio transport an MCP server left running on the user's machine.
func TestMCPGatewayBuiltAfterSessionCloseIsTornDown(t *testing.T) {
	sessionID := "sid-closed-build"
	backend, transport := newTunnelPair(t, sessionID, nil)
	agent := backend.agent

	connParams := agent.connectionParams(sessionID)
	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionTypeMcpProxy)
	if err != nil {
		t.Fatalf("env: %v", err)
	}

	// Cleanup runs first and finds nothing: this is the build-in-flight state.
	agent.sessionCleanup(sessionID)

	gw, err := agent.mcpGatewayFor(sessionID, connenv, connParams,
		mcpProxyOpts(connenv, connParams, sessionID, "1"),
		map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)})
	if err == nil {
		t.Fatal("mcpGatewayFor handed out a gateway for a session that is already gone")
	}
	if gw != nil {
		t.Fatal("a gateway was returned alongside the error")
	}
	if _, ok := agent.mcpGateways.Load(sessionID); ok {
		t.Fatal("the gateway built after cleanup stayed memoised; nothing will ever close it")
	}

	// The teardown has to be real, not just an unregistration: the client must
	// have been asked to reap the MCP server the build spawned on its machine.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(transport.packetsOfType(pbclient.MCPStdioClose)) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no MCPStdioClose reached the client; the mcp server built after cleanup is still running on the user's machine")
}

// A packet that arrives after session cleanup must do nothing at all.
//
// Everything handleMCPProxyWrite does past its guard is constructive: it
// builds a gateway, starts a sink goroutine, and on this transport spawns an
// MCP server on the user's machine. Doing any of that for a session whose
// teardown has already run creates state that nothing will ever close, because
// cleanup does not run twice.
//
// "Nothing" includes talking to the gateway. A handler that runs and then
// fails somewhere downstream reports the failure as a SessionClose, which for
// an already-closed session is a second close carrying an error the user never
// caused — their MCP client renders it as the reason their working session
// ended. Dropping the packet is the only correct answer.
func TestLateMCPWriteAfterCleanupIsANoOp(t *testing.T) {
	sessionID := "sid-late-write"
	backend, transport := newTunnelPair(t, sessionID, nil)
	agent := backend.agent

	agent.sessionCleanup(sessionID)
	before := len(transport.packetsOfType(pbclient.MCPStdioRequest))

	agent.handleMCPProxyWrite(&pb.Packet{
		Type: pbclient.MCPProxyConnectionWrite,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(sessionID),
			pb.SpecClientConnectionID: []byte("conn-late"),
		},
		Payload: []byte("POST /mcp HTTP/1.1\r\nContent-Length: 0\r\n\r\n"),
	})

	if _, ok := agent.mcpGateways.Load(sessionID); ok {
		t.Fatal("a packet arriving after cleanup rebuilt the session's gateway; its sink goroutine now outlives the session")
	}
	if got := agent.connStore.Get(sessionID + ":conn-late"); got != nil {
		t.Fatal("a packet arriving after cleanup built a proxy that nothing will close")
	}
	if after := len(transport.packetsOfType(pbclient.MCPStdioRequest)); after != before {
		t.Fatalf("a packet arriving after cleanup sent %d stdio requests; it would spawn an MCP server on the user's machine with no reaping packet left to stop it", after-before)
	}
	if n := len(transport.packetsOfType(pbclient.SessionClose)); n != 0 {
		t.Fatalf("a packet arriving after cleanup sent %d SessionClose packets; the session is already closed and the user's client renders the error as the reason it ended", n)
	}
	if n := len(transport.packetsOfType(pbclient.TCPConnectionClose)); n != 0 {
		t.Fatalf("a packet arriving after cleanup sent %d TCPConnectionClose packets for a connection it never opened", n)
	}
}
