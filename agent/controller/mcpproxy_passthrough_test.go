package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/agent/config"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/libhoop/agent/mcpadapter"
)

// recordingMCPServer is a remote MCP server that answers initialize and
// tools/call and remembers the Authorization header each request arrived with.
//
// The header is the whole assertion: passthrough is only real if the token the
// caller sent is the token the server sees.
type recordingMCPServer struct {
	srv *httptest.Server

	mu   sync.Mutex
	auth []string
	// upstreamHeader records any X-Hoop-Upstream-Authorization that leaked
	// through to the server. It must always be empty: that header addresses
	// hoop, not the MCP server, and forwarding it would disclose the caller's
	// credential twice under two names.
	upstreamHeader []string
}

func newRecordingMCPServer(t *testing.T) *recordingMCPServer {
	t.Helper()
	s := &recordingMCPServer{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		if v := r.Header.Get(mcpUpstreamAuthHeader); v != "" {
			s.upstreamHeader = append(s.upstreamHeader, v)
		}
		s.mu.Unlock()

		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var env struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &env)

		w.Header().Set("Content-Type", "application/json")
		switch env.Method {
		case "initialize":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"recording-mcp","version":"1"}}}`,
				jsonID(env.ID))
		case "tools/call":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"ok"}]}}`,
				jsonID(env.ID))
		default:
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{}}`, jsonID(env.ID))
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *recordingMCPServer) seen() (auth, leaked []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.auth...), append([]string(nil), s.upstreamHeader...)
}

func jsonID(id any) string {
	raw, err := json.Marshal(id)
	if err != nil || id == nil {
		return "null"
	}
	return string(raw)
}

// newPassthroughGateway builds the real agent-side mcpproxy gateway for a
// passthrough connection pointed at srv.
func newPassthroughGateway(t *testing.T, srv *recordingMCPServer, sessionID string, envs map[string]string) http.Handler {
	t.Helper()
	agent := New(&loopTransport{}, &config.Config{}, nil)

	base := map[string]string{
		"MCP_TRANSPORT": "streamable-http",
		"REMOTE_URL":    srv.srv.URL,
		"MCP_AUTH":      "passthrough",
	}
	connenv, err := parseConnectionEnvVars(mcpProxyEnvVars(mergeEnv(base, envs)), pb.ConnectionTypeMcpProxy)
	if err != nil {
		t.Fatalf("passthrough connection rejected by the agent: %v", err)
	}

	gw, err := agent.buildMCPGateway(connenv, &pb.AgentConnectionParams{
		ConnectionName: "gh-mcp",
		ConnectionType: string(pb.ConnectionTypeMcpProxy),
		UserID:         "user-1",
		UserEmail:      "dev@example.com",
	}, sessionID, mcpadapter.Hooks{}, nil)
	if err != nil {
		t.Fatalf("building the mcp gateway: %v", err)
	}
	t.Cleanup(gw.Close)
	return gw.Handler()
}

// call drives one JSON-RPC message through the gateway exactly as an MCP
// client would, with the caller's own upstream credential on the passthrough
// header. Returns the response body and the MCP session id.
func call(t *testing.T, h http.Handler, session, upstreamToken, body string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if upstreamToken != "" {
		req.Header.Set(mcpUpstreamAuthHeader, upstreamToken)
	}
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() { defer close(done); h.ServeHTTP(rec, req) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("gateway did not answer within 20s")
	}
	sid := rec.Header().Get("Mcp-Session-Id")
	if sid == "" {
		sid = session
	}
	return rec, sid
}

// The credential a user's MCP client sends must be the credential the MCP
// server receives. This is the entire feature: without it every user of the
// connection authenticates upstream as the same shared identity.
func TestPassthroughForwardsTheCallersOwnCredential(t *testing.T) {
	srv := newRecordingMCPServer(t)
	h := newPassthroughGateway(t, srv, "sid-passthrough", nil)

	_, session := call(t, h, "", "Bearer user-one-token",
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if session == "" {
		t.Fatal("initialize returned no mcp session id")
	}
	rec, _ := call(t, h, session, "Bearer user-one-token",
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("tools/call returned %d: %s", rec.Code, rec.Body.String())
	}

	auth, leaked := srv.seen()
	if len(auth) == 0 {
		t.Fatal("the mcp server was never reached")
	}
	for i, got := range auth {
		if got != "Bearer user-one-token" {
			t.Fatalf("request %d reached the server with Authorization %q, want the caller's own token", i, got)
		}
	}
	// The passthrough header addresses hoop, not the MCP server. Forwarding
	// it would hand the backend the same secret twice under a second name.
	if len(leaked) != 0 {
		t.Fatalf("the passthrough header reached the mcp server: %v", leaked)
	}
}

// Two users on ONE connection must reach the server as themselves. A shared
// gateway that cached the first caller's token would make the second user
// silently act as the first — the exact failure passthrough exists to prevent,
// and one that a single-user test cannot see.
func TestPassthroughKeepsUsersApartOnOneConnection(t *testing.T) {
	srv := newRecordingMCPServer(t)
	h := newPassthroughGateway(t, srv, "sid-two-users", nil)

	_, aliceSession := call(t, h, "", "Bearer alice-token",
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	_, bobSession := call(t, h, "", "Bearer bob-token",
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if aliceSession == bobSession {
		t.Fatalf("both callers share mcp session %q", aliceSession)
	}

	call(t, h, aliceSession, "Bearer alice-token",
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami"}}`)
	call(t, h, bobSession, "Bearer bob-token",
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami"}}`)

	auth, _ := srv.seen()
	var alice, bob int
	for _, got := range auth {
		switch got {
		case "Bearer alice-token":
			alice++
		case "Bearer bob-token":
			bob++
		default:
			t.Fatalf("the server saw an unexpected credential %q", got)
		}
	}
	if alice == 0 || bob == 0 {
		t.Fatalf("each caller must reach the server as themselves; alice=%d bob=%d (%v)", alice, bob, auth)
	}
}

// A caller that sends no credential must be refused rather than reaching the
// server anonymously. Succeeding as nobody, or as whoever the connection
// happens to hold, is worse than a clear failure.
func TestPassthroughRefusesACallerWithNoCredential(t *testing.T) {
	srv := newRecordingMCPServer(t)
	h := newPassthroughGateway(t, srv, "sid-no-cred", nil)

	_, session := call(t, h, "", "Bearer someone",
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	before, _ := srv.seen()

	rec, _ := call(t, h, session, "",
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami"}}`)
	if body := rec.Body.String(); !strings.Contains(body, mcpUpstreamAuthHeader) {
		t.Fatalf("a credential-less call did not name the missing header: %s", body)
	}

	after, _ := srv.seen()
	if len(after) != len(before) {
		t.Fatalf("a credential-less call still reached the server as %q", after[len(after)-1])
	}
}

// Static mode must be untouched by the passthrough wiring: the connection's
// own credential still authenticates, and a client-supplied passthrough header
// must NOT override it. Otherwise any caller could substitute their own
// identity on a connection the admin configured as shared.
func TestStaticModeIgnoresTheClientSuppliedCredential(t *testing.T) {
	srv := newRecordingMCPServer(t)
	agent := New(&loopTransport{}, &config.Config{}, nil)

	connenv, err := parseConnectionEnvVars(mcpProxyEnvVars(map[string]string{
		"MCP_TRANSPORT":        "streamable-http",
		"REMOTE_URL":           srv.srv.URL,
		"MCP_AUTH":             "static",
		"HEADER_AUTHORIZATION": "Bearer connection-token",
	}), pb.ConnectionTypeMcpProxy)
	if err != nil {
		t.Fatalf("static connection rejected: %v", err)
	}
	gw, err := agent.buildMCPGateway(connenv, &pb.AgentConnectionParams{
		ConnectionName: "gh-mcp",
		ConnectionType: string(pb.ConnectionTypeMcpProxy),
		UserID:         "user-1",
	}, "sid-static", mcpadapter.Hooks{}, nil)
	if err != nil {
		t.Fatalf("building the mcp gateway: %v", err)
	}
	t.Cleanup(gw.Close)

	h := gw.Handler()
	_, session := call(t, h, "", "Bearer attacker-token",
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	call(t, h, session, "Bearer attacker-token",
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami"}}`)

	auth, _ := srv.seen()
	if len(auth) == 0 {
		t.Fatal("the mcp server was never reached")
	}
	for i, got := range auth {
		if got != "Bearer connection-token" {
			t.Fatalf("request %d authenticated as %q; a client header overrode the connection credential", i, got)
		}
	}
}

// A bare token (no "Bearer " prefix) must work: MCP clients disagree about
// whether to include it, and the upstream needs a well-formed header either
// way.
func TestPassthroughAcceptsABareToken(t *testing.T) {
	srv := newRecordingMCPServer(t)
	h := newPassthroughGateway(t, srv, "sid-bare", nil)

	_, session := call(t, h, "", "raw-token-no-prefix",
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	call(t, h, session, "raw-token-no-prefix",
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami"}}`)

	auth, _ := srv.seen()
	for i, got := range auth {
		if got != "Bearer raw-token-no-prefix" {
			t.Fatalf("request %d sent %q, want the token normalised to a bearer header", i, got)
		}
	}
}

// Deny/allow policy still applies in passthrough mode: authenticating as
// yourself does not mean the gateway stops enforcing what you may call.
func TestPassthroughStillEnforcesToolPolicy(t *testing.T) {
	srv := newRecordingMCPServer(t)
	h := newPassthroughGateway(t, srv, "sid-policy", map[string]string{
		"MCP_DENIED_TOOLS": "whoami",
	})

	_, session := call(t, h, "", "Bearer user-token",
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	before, _ := srv.seen()

	rec, _ := call(t, h, session, "Bearer user-token",
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami"}}`)
	if !strings.Contains(rec.Body.String(), "error") {
		t.Fatalf("a denied tool was not refused: %s", rec.Body.String())
	}

	after, _ := srv.seen()
	if len(after) != len(before) {
		t.Fatal("a denied tool call still reached the mcp server")
	}
}
