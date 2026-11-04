package mcpgateway_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/mcpgateway"
	"github.com/hoophq/hoop/sidecar/mcpgateway/internal/testupstream"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// recSession is the sidecar session a test lane opens: it records what the
// engine hands it, denies the tools in deny and hides the tools in hide.
type recSession struct {
	mu     sync.Mutex
	id     string
	deny   map[string]string
	hide   map[string]bool
	calls  []string
	acts   []string
	closed bool
}

func (s *recSession) ID() string { return s.id }

func (s *recSession) ToolCall(_ context.Context, tool string, _ []byte) daemon.MCPVerdict {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, tool)
	if rule, ok := s.deny[tool]; ok {
		return daemon.MCPVerdict{Message: "not allowed", Rule: rule}
	}
	return daemon.MCPVerdict{Allowed: true}
}

func (s *recSession) Hidden(tool string) bool { return s.hide[tool] }

func (s *recSession) ToolList(_ context.Context, tools []daemon.MCPToolInfo, hidden []string) daemon.MCPVerdict {
	names := make([]string, 0, len(tools))
	for _, tl := range tools {
		names = append(names, tl.Name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acts = append(s.acts, "tools/list:"+strings.Join(names, ",")+" hidden:"+strings.Join(hidden, ","))
	return daemon.MCPVerdict{Allowed: true}
}

func (s *recSession) Record(_ context.Context, activity string, _ map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acts = append(s.acts, activity)
}

func (s *recSession) Close(context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
}

func (s *recSession) snapshot() (calls, acts []string, closed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...), append([]string(nil), s.acts...), s.closed
}

// testLane opens one recSession per MCP session.
type testLane struct {
	mu       sync.Mutex
	deny     map[string]string
	hide     map[string]bool
	sessions []*recSession
}

func (l *testLane) open(_ context.Context, c daemon.MCPCaller) (daemon.MCPLaneSession, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := &recSession{id: fmt.Sprintf("sidecar-%d", len(l.sessions)+1), deny: l.deny, hide: l.hide}
	l.sessions = append(l.sessions, s)
	return s, nil
}

func (l *testLane) session(i int) *recSession {
	l.mu.Lock()
	defer l.mu.Unlock()
	if i >= len(l.sessions) {
		return nil
	}
	return l.sessions[i]
}

func (l *testLane) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.sessions)
}

// startLane serves the engine on a loopback port in front of a testupstream
// server and returns the MCP endpoint.
func startLane(t *testing.T, policy daemon.MCPGatewayConfig, open func(context.Context, daemon.MCPCaller) (daemon.MCPLaneSession, error)) (daemon.MCPGatewayServer, string) {
	t.Helper()
	return startLaneOn(t, testupstream.NewServer(""), policy, open)
}

// startLaneOn is startLane in front of the given upstream server.
func startLaneOn(t *testing.T, upstream *sdk.Server, policy daemon.MCPGatewayConfig, open func(context.Context, daemon.MCPCaller) (daemon.MCPLaneSession, error)) (daemon.MCPGatewayServer, string) {
	t.Helper()
	up := httptest.NewServer(testupstream.Handler(upstream))
	t.Cleanup(up.Close)
	return startLaneAt(t, up.URL+"/mcp", policy, open)
}

// startLaneAt is startLane in front of any upstream URL.
func startLaneAt(t *testing.T, upstream string, policy daemon.MCPGatewayConfig, open func(context.Context, daemon.MCPCaller) (daemon.MCPLaneSession, error)) (daemon.MCPGatewayServer, string) {
	t.Helper()
	srv, err := mcpgateway.Build(daemon.MCPGatewayLane{Name: "u", Listen: "127.0.0.1:0", Upstream: upstream, Transport: daemon.MCPTransportStreamableHTTP, Policy: policy, Open: open})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	for i := 0; i < 200; i++ {
		if a := srv.Addr(); a != nil && a.String() != "127.0.0.1:0" {
			return srv, "http://" + a.String() + "/mcp"
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the lane did not bind")
	return nil, ""
}

type client struct {
	url, sid, version string
}

func (c *client) post(t *testing.T, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, c.url, bytes.NewBufferString(body))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json, text/event-stream")
	if c.sid != "" {
		req.Header.Set("Mcp-Session-Id", c.sid)
	}
	if c.sid != "" && c.version != "" {
		req.Header.Set("MCP-Protocol-Version", c.version)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sid = sid
	}
	return resp.StatusCode, string(b)
}

func (c *client) delete(t *testing.T) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, c.url, nil)
	req.Header.Set("Mcp-Session-Id", c.sid)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func (c *client) init(t *testing.T) {
	t.Helper()
	code, body := c.post(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	if code != 200 || c.sid == "" || strings.Contains(body, `"error"`) {
		t.Fatalf("initialize: %d %s", code, body)
	}
	c.post(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
}

func (c *client) list(t *testing.T) string {
	t.Helper()
	code, body := c.post(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if code != 200 {
		t.Fatalf("tools/list: %d %s", code, body)
	}
	return body
}

func (c *client) call(t *testing.T, id int, tool, args string) string {
	t.Helper()
	code, body := c.post(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, id, tool, args))
	if code != 200 {
		t.Fatalf("tools/call %s: %d %s", tool, code, body)
	}
	return body
}

// waitFor polls cond for up to two seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A denied tool is missing from tools/list, and a call to it is refused by
// mcpproxy before the gate sees it; the refusal reaches the sidecar session.
// A client that negotiated an older revision echoes it on every later request.
func TestNegotiatedOlderRevisionIsAccepted(t *testing.T) {
	for _, rev := range []string{"2025-06-18", "2025-03-26"} {
		t.Run(rev, func(t *testing.T) {
			ln := &testLane{}
			_, url := startLane(t, daemon.MCPGatewayConfig{}, ln.open)
			c := &client{url: url}
			code, body := c.post(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+rev+`","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
			if code != http.StatusOK || !strings.Contains(body, `"protocolVersion":"`+rev+`"`) {
				t.Fatalf("initialize: %d %s", code, body)
			}
			c.version = rev
			if catalog := c.list(t); !strings.Contains(catalog, `"echo"`) {
				t.Fatalf("tools/list on %s: %s", rev, catalog)
			}
			c.version = "2025-11-25"
			if code, body := c.post(t, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`); code != http.StatusBadRequest {
				t.Fatalf("tools/list echoing 2025-11-25 after negotiating %s: %d %s, want 400", rev, code, body)
			}
		})
	}
}

// A tool an mcp_tool rule denies is absent from tools/list, and a call to it
// anyway reaches the rule, which answers a tool error under its own name.
func TestAHiddenToolIsAbsentFromTheCatalogAndRefused(t *testing.T) {
	ln := &testLane{deny: map[string]string{"delete_customer": "no-delete"}, hide: map[string]bool{"delete_customer": true}}
	_, url := startLane(t, daemon.MCPGatewayConfig{}, ln.open)
	c := &client{url: url}
	c.init(t)
	catalog := c.list(t)
	if strings.Contains(catalog, "delete_customer") || !strings.Contains(catalog, `"echo"`) {
		t.Fatalf("catalog: %s", catalog)
	}
	body := c.call(t, 3, "delete_customer", `{"id":"7"}`)
	if !strings.Contains(body, `"isError":true`) || !strings.Contains(body, "(rule no-delete)") {
		t.Fatalf("a hidden tool answered: %s", body)
	}
	calls, acts, _ := ln.session(0).snapshot()
	if strings.Join(calls, ",") != "delete_customer" {
		t.Fatalf("statements %v, want the refused call", calls)
	}
	if !strings.Contains(strings.Join(acts, "\n"), "hidden:delete_customer") {
		t.Fatalf("the catalog statement does not name the hidden tool: %v", acts)
	}
}

// A change to a hidden tool between two tools/list calls is no rug pull: the
// client never saw the tool, so the session goes on. A change to a listed
// tool still ends it.
func TestAChangeToAHiddenToolIsNoRugPull(t *testing.T) {
	up := testupstream.NewServer("")
	ln := &testLane{hide: map[string]bool{"delete_customer": true}}
	_, url := startLaneOn(t, up, daemon.MCPGatewayConfig{}, ln.open)
	c := &client{url: url}
	c.init(t)
	c.list(t)
	redefine := func(name string) {
		sdk.AddTool(up, &sdk.Tool{Name: name, Description: "Changed after the first tools/list."},
			func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
				return nil, nil, nil
			})
	}
	redefine("delete_customer")
	if body := c.list(t); !strings.Contains(body, `"echo"`) || strings.Contains(body, "delete_customer") {
		t.Fatalf("tools/list after a hidden tool changed: %s", body)
	}
	redefine("echo")
	if body := c.list(t); strings.Contains(body, `"tools"`) || !strings.Contains(body, `"error"`) {
		t.Fatalf("tools/list after a listed tool changed: %s", body)
	}
}

// The catalog filter reads the real tool name, so a tool overrides renamed is
// hidden by a rule that names it.
func TestARenamedToolIsHiddenByItsRealName(t *testing.T) {
	ln := &testLane{hide: map[string]bool{"customer_lookup": true}}
	policy := daemon.MCPGatewayConfig{Overrides: map[string]daemon.MCPToolOverride{"customer_lookup": {Name: "find_customer"}}}
	_, url := startLane(t, policy, ln.open)
	c := &client{url: url}
	c.init(t)
	if catalog := c.list(t); strings.Contains(catalog, "find_customer") || strings.Contains(catalog, "customer_lookup") {
		t.Fatalf("catalog: %s", catalog)
	}
	if _, acts, _ := ln.session(0).snapshot(); !strings.Contains(strings.Join(acts, "\n"), "hidden:customer_lookup") {
		t.Fatalf("acts: %v", acts)
	}
}

// A tools/call is judged by its exact "name" key, as an MCP server reads it.
// Params whose keys differ only in case are refused: Go reads them without
// case, so the lane would judge one tool and a server in another language
// would run the other.
func TestAToolCallIsJudgedByItsExactNameKey(t *testing.T) {
	ln := &testLane{deny: map[string]string{"delete_customer": "no-delete"}, hide: map[string]bool{"delete_customer": true}}
	_, url := startLane(t, daemon.MCPGatewayConfig{}, ln.open)
	c := &client{url: url}
	c.init(t)
	c.list(t)
	for i, params := range []string{
		`{"name":"delete_customer","Name":"echo","arguments":{"message":"x"}}`,
		`{"Name":"echo","name":"delete_customer","arguments":{"message":"x"}}`,
		`{"name":"echo","arguments":{"message":"x"},"ARGUMENTS":{"message":"y"}}`,
		`{"Name":"echo","arguments":{"message":"x"}}`,
		`{"name":"echo","arguments":{"message":"x"},"argumentſ":{"message":"y"}}`,
	} {
		code, body := c.post(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":%s}`, 10+i, params))
		if code != 200 || !strings.Contains(body, "-32602") {
			t.Errorf("params %s: %d %s", params, code, body)
		}
	}
	if calls, _, _ := ln.session(0).snapshot(); len(calls) != 0 {
		t.Fatalf("the lane judged %v, want no call", calls)
	}
}

// A refused call to a hidden tool counts toward max_calls_per_session, as any
// refused call does.
func TestAHiddenToolCallCountsTowardTheBudget(t *testing.T) {
	ln := &testLane{deny: map[string]string{"delete_customer": "no-delete"}, hide: map[string]bool{"delete_customer": true}}
	_, url := startLane(t, daemon.MCPGatewayConfig{MaxCallsPerSession: 2}, ln.open)
	c := &client{url: url}
	c.init(t)
	c.list(t)
	c.call(t, 3, "echo", `{"message":"one"}`)
	if body := c.call(t, 4, "delete_customer", `{"id":"7"}`); !strings.Contains(body, "(rule no-delete)") {
		t.Fatalf("hidden call: %s", body)
	}
	if body := c.call(t, 5, "echo", `{"message":"three"}`); !strings.Contains(body, "-32012") {
		t.Fatalf("third call: %s, want the budget refusal", body)
	}
}

// The upstream client follows a redirect within the upstream's origin and
// refuses one that leaves it.
func TestARedirectStaysWithinTheUpstreamOrigin(t *testing.T) {
	up := testupstream.Handler(testupstream.NewServer(""))
	other := httptest.NewServer(up)
	t.Cleanup(other.Close)
	mux := http.NewServeMux()
	mux.Handle("/mcp", up)
	mux.Handle("/old", http.RedirectHandler("/mcp", http.StatusTemporaryRedirect))
	mux.Handle("/away", http.RedirectHandler(other.URL+"/mcp", http.StatusTemporaryRedirect))
	origin := httptest.NewServer(mux)
	t.Cleanup(origin.Close)

	_, url := startLaneAt(t, origin.URL+"/old", daemon.MCPGatewayConfig{}, (&testLane{}).open)
	(&client{url: url}).init(t)

	_, url = startLaneAt(t, origin.URL+"/away", daemon.MCPGatewayConfig{}, (&testLane{}).open)
	c := &client{url: url}
	if _, body := c.post(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`); !strings.Contains(body, `"error"`) {
		t.Fatalf("initialize through a redirect off the origin: %s", body)
	}
}

// A call to a hidden tool under the name overrides gives it is denied under
// the rule too, though the client never saw that name.
func TestARenamedHiddenToolIsDeniedUnderItsNewName(t *testing.T) {
	ln := &testLane{deny: map[string]string{"customer_lookup": "no-lookup"}, hide: map[string]bool{"customer_lookup": true}}
	policy := daemon.MCPGatewayConfig{Overrides: map[string]daemon.MCPToolOverride{"customer_lookup": {Name: "find_customer"}}}
	_, url := startLane(t, policy, ln.open)
	c := &client{url: url}
	c.init(t)
	c.list(t)
	body := c.call(t, 3, "find_customer", `{"id":"7"}`)
	if !strings.Contains(body, `"isError":true`) || !strings.Contains(body, "(rule no-lookup)") {
		t.Fatalf("call under the new name: %s", body)
	}
	if calls, _, _ := ln.session(0).snapshot(); strings.Join(calls, ",") != "customer_lookup" {
		t.Fatalf("statements %v, want the real name", calls)
	}
}

// A call a sidecar rule denies answers as a tool error, so the agent keeps
// the server; an allowed call goes through. One
// statement per call, either way.
func TestARuleDenialIsAToolError(t *testing.T) {
	ln := &testLane{deny: map[string]string{"delete_customer": "no-delete"}}
	_, url := startLane(t, daemon.MCPGatewayConfig{}, ln.open)
	c := &client{url: url}
	c.init(t)
	body := c.call(t, 3, "delete_customer", `{"id":"7"}`)
	if !strings.Contains(body, `"isError":true`) || !strings.Contains(body, "denied by the hoop sidecar (rule no-delete): not allowed") || strings.Contains(body, `"error"`) {
		t.Fatalf("denied call: %s", body)
	}
	if body := c.call(t, 4, "echo", `{"message":"hi"}`); !strings.Contains(body, `"hi"`) || strings.Contains(body, "isError") {
		t.Fatalf("allowed call: %s", body)
	}
	calls, acts, _ := ln.session(0).snapshot()
	if strings.Join(calls, ",") != "delete_customer,echo" {
		t.Fatalf("statements %v, want one per call", calls)
	}
	if !strings.Contains(strings.Join(acts, "\n"), "mcp_initialize") {
		t.Fatalf("initialize not recorded: %v", acts)
	}
}

// One sidecar session per MCP session, closed when the client ends it.
func TestOneSidecarSessionPerMCPSession(t *testing.T) {
	ln := &testLane{}
	srv, url := startLane(t, daemon.MCPGatewayConfig{}, ln.open)
	a, b := &client{url: url}, &client{url: url}
	a.init(t)
	b.init(t)
	if ln.count() != 2 || a.sid == b.sid || ln.session(0).ID() == ln.session(1).ID() {
		t.Fatalf("sessions: %d, sids %q %q", ln.count(), a.sid, b.sid)
	}
	if code := a.delete(t); code != 204 {
		t.Fatalf("DELETE: %d", code)
	}
	waitFor(t, "session a to close", func() bool { _, _, closed := ln.session(0).snapshot(); return closed })
	if _, _, closed := ln.session(1).snapshot(); closed {
		t.Fatal("session b closed with a")
	}
	if active, total, _ := srv.Stats(); active != 1 || total != 2 {
		t.Fatalf("stats active=%d total=%d", active, total)
	}
}

// EndSessions closes the sidecar sessions now; the MCP session is refused
// from its next message on, then gone, so the client initializes again.
func TestEndSessionsRefusesTheNextMessageUntilTheClientInitializesAgain(t *testing.T) {
	ln := &testLane{}
	srv, url := startLane(t, daemon.MCPGatewayConfig{}, ln.open)
	c := &client{url: url}
	c.init(t)
	srv.EndSessions("rules reloaded")
	_, acts, closed := ln.session(0).snapshot()
	if !closed || !strings.Contains(strings.Join(acts, "\n"), "rules_reloaded") {
		t.Fatalf("session after EndSessions: closed=%v acts=%v", closed, acts)
	}
	code, body := c.post(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if code != 200 || !strings.Contains(body, `"error"`) || !strings.Contains(body, "initialize a new session") {
		t.Fatalf("first message after the reload: %d %s", code, body)
	}
	if code, _ := c.post(t, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`); code != 404 {
		t.Fatalf("second message after the reload: %d, want 404", code)
	}
	c.sid = ""
	c.init(t)
	if ln.count() != 2 {
		t.Fatalf("sessions after a new initialize: %d, want 2", ln.count())
	}
	if body := c.call(t, 4, "echo", `{"message":"again"}`); !strings.Contains(body, "again") {
		t.Fatalf("call on the new session: %s", body)
	}
}

// A lane built with no Open (a validate run) serves no session: every
// request is refused rather than forwarded unrecorded.
func TestALaneWithoutASessionOpenerRefusesEveryRequest(t *testing.T) {
	_, url := startLane(t, daemon.MCPGatewayConfig{}, nil)
	c := &client{url: url}
	code, body := c.post(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	if code != 200 || !strings.Contains(body, "sidecar session unavailable") {
		t.Fatalf("initialize without an opener: %d %s", code, body)
	}
}

// A tools/call whose params name no tool is refused as invalid params; no
// rule judges it and the upstream never sees it.
func TestAToolCallWithoutAToolNameIsRefused(t *testing.T) {
	ln := &testLane{}
	_, url := startLane(t, daemon.MCPGatewayConfig{}, ln.open)
	c := &client{url: url}
	c.init(t)
	for i, params := range []string{`{"name":7}`, `{"arguments":{}}`, `[]`} {
		code, body := c.post(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":%s}`, 10+i, params))
		if code != 200 || !strings.Contains(body, "-32602") {
			t.Errorf("params %s: %d %s", params, code, body)
		}
	}
	if calls, _, _ := ln.session(0).snapshot(); len(calls) != 0 {
		t.Fatalf("the lane judged %v", calls)
	}
}
