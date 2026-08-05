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

	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/common/proto/spectypes"
)

// fakeOpenAI is an OpenAI-compatible chat-completions endpoint that always
// answers with one risk-tool call.
//
// Pointing the real engine at it (via AISessionAnalyzerParams.APIURL) is what
// makes the test end-to-end: the provider client, the tool-calling contract and
// the risk-tier mapping all run for real, and only the model is fake.
type fakeOpenAI struct {
	srv  *httptest.Server
	tool string

	mu       sync.Mutex
	prompts  []string
	requests int
}

func newFakeOpenAI(t *testing.T, riskTool string) *fakeOpenAI {
	t.Helper()
	f := &fakeOpenAI{tool: riskTool}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests++
		f.prompts = append(f.prompts, string(body))
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
		  "id": "chatcmpl-1",
		  "object": "chat.completion",
		  "created": 1,
		  "model": "gpt-4o",
		  "choices": [{
		    "index": 0,
		    "message": {
		      "role": "assistant",
		      "tool_calls": [{
		        "id": "call_1",
		        "type": "function",
		        "function": {
		          "name": %q,
		          "arguments": "{\"title\":\"Destructive delete\",\"explanation\":\"Drops a table outright.\"}"
		        }
		      }]
		    },
		    "finish_reason": "tool_calls"
		  }]
		}`, f.tool)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeOpenAI) seen() (int, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests, append([]string(nil), f.prompts...)
}

// analyzerParams builds the config the gateway resolves and ships to the agent,
// pointed at the fake provider.
func analyzerParams(apiURL, highRiskAction string) *pb.AISessionAnalyzerParams {
	return &pb.AISessionAnalyzerParams{
		RuleName:         "mcp-rule",
		Provider:         "openai",
		APIURL:           apiURL,
		APIKey:           "test-key",
		Model:            "gpt-4o",
		LowRiskAction:    "allow_execution",
		MediumRiskAction: "allow_execution",
		HighRiskAction:   highRiskAction,
	}
}

// End to end: a high-risk tool call must be refused by the real pipeline, and
// the verdict must reach the gateway on the wire.
//
// Everything but the model runs for real here — the mcpproxy gateway, the
// assembled pipeline, the provider client, the tunnel to a stdio MCP server on
// the "user's machine". Unit tests on the check prove its logic; only this
// proves it is actually wired in.
func TestMCPAnalyzerBlocksToolCallEndToEnd(t *testing.T) {
	sessionID := "sid-analyzer-e2e"
	backend, transport := newTunnelPair(t, sessionID, nil)
	agent := backend.agent
	t.Cleanup(func() { agent.closeMCPProxyConnections(sessionID) })

	fake := newFakeOpenAI(t, "HighRiskAISessionAnalyzer")

	connParams := agent.connectionParams(sessionID)
	connParams.AISessionAnalyzer = analyzerParams(fake.srv.URL, "block_execution")
	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionTypeMcpProxy)
	if err != nil {
		t.Fatalf("env: %v", err)
	}

	spec := map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)}
	gw, err := agent.mcpGatewayFor(sessionID, connenv, connParams,
		mcpProxyOpts(connenv, connParams, sessionID, "1"), spec)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}

	h := gw.Handler()
	_, sid, _ := mcpPost(t, h, "", mcpInitialize)
	if sid == "" {
		t.Fatal("initialize did not open a session")
	}
	mcpPost(t, h, sid, mcpToolsList)

	// tools/list must not have cost a model call: only tool calls are analyzed.
	if n, _ := fake.seen(); n != 0 {
		t.Fatalf("the analyzer spent %d model calls before any tools/call", n)
	}

	_, _, body := mcpPost(t, h, sid,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"whoami","arguments":{}}}`)

	var envelope map[string]any
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("tools/call reply is not json: %s", body)
	}
	if _, denied := envelope["error"]; !denied {
		t.Fatalf("a high-risk tool call executed on the user's machine: %s", body)
	}
	if !strings.Contains(body, "ai session analyzer") {
		t.Errorf("denial does not name the analyzer: %s", body)
	}
	// The tool must not have run: its result reports a pid.
	if strings.Contains(body, "ran on pid") {
		t.Fatalf("the blocked tool executed anyway: %s", body)
	}

	n, prompts := fake.seen()
	if n != 1 {
		t.Fatalf("provider saw %d requests, want exactly 1 for one tool call", n)
	}
	// The model must have been shown the tool call, not the HTTP envelope.
	if !strings.Contains(prompts[0], "whoami") {
		t.Errorf("the model was not shown the tool name: %s", prompts[0])
	}

	// The verdict has to reach the gateway, or the session metrics stay empty.
	//
	// Close first: verdicts ride the audit sink's queue and are written by its
	// drain goroutine, so reading the transport straight after the tool call
	// races that goroutine. closeMCPProxyConnections flushes it and waits.
	agent.closeMCPProxyConnections(sessionID)

	var verdicts [][]byte
	for _, pkt := range transport.packetsOfType(pbclient.MCPProxyConnectionWrite) {
		if raw := pkt.Spec[spectypes.AIAnalyzerInfoKey]; len(raw) > 0 {
			if len(pkt.Spec[pb.SpecMCPEventKey]) == 0 {
				t.Error("verdict packet is not tagged as an MCP event; the gateway would forward it to the client")
			}
			verdicts = append(verdicts, raw)
		}
	}
	if len(verdicts) != 1 {
		t.Fatalf("%d verdicts reached the gateway, want 1", len(verdicts))
	}
	v := decodeVerdict(t, verdicts[0])
	if v.Outcome != "block" || v.RiskLevel != "high" {
		t.Errorf("verdict = %+v, want a high-risk block", v)
	}
	if v.RuleName != "mcp-rule" {
		t.Errorf("verdict rule = %q, want the configured rule name", v.RuleName)
	}
}

// The same high-risk classification with the tier configured to allow must let
// the call through. Without this, a test suite passes just as well against a
// check that blocks everything it analyzes.
func TestMCPAnalyzerRespectsTheConfiguredRiskAction(t *testing.T) {
	sessionID := "sid-analyzer-allow"
	backend, transport := newTunnelPair(t, sessionID, nil)
	agent := backend.agent
	t.Cleanup(func() { agent.closeMCPProxyConnections(sessionID) })

	fake := newFakeOpenAI(t, "HighRiskAISessionAnalyzer")

	connParams := agent.connectionParams(sessionID)
	// High risk, but the operator chose to allow rather than block.
	connParams.AISessionAnalyzer = analyzerParams(fake.srv.URL, "allow_execution")
	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionTypeMcpProxy)
	if err != nil {
		t.Fatalf("env: %v", err)
	}

	gw, err := agent.mcpGatewayFor(sessionID, connenv, connParams,
		mcpProxyOpts(connenv, connParams, sessionID, "1"),
		map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)})
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}

	h := gw.Handler()
	_, sid, _ := mcpPost(t, h, "", mcpInitialize)
	mcpPost(t, h, sid, mcpToolsList)
	_, _, body := mcpPost(t, h, sid,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"whoami","arguments":{}}}`)

	if !strings.Contains(body, "ran on pid") {
		t.Fatalf("an allowed tool call did not reach the server: %s", body)
	}
	// Allowed, but still recorded: the metrics count analyzed calls, not just
	// blocked ones. Close first so the sink's queue has been drained (see
	// TestMCPAnalyzerBlocksToolCallEndToEnd).
	agent.closeMCPProxyConnections(sessionID)

	var verdicts int
	for _, pkt := range transport.packetsOfType(pbclient.MCPProxyConnectionWrite) {
		if len(pkt.Spec[spectypes.AIAnalyzerInfoKey]) > 0 {
			verdicts++
		}
	}
	if verdicts != 1 {
		t.Fatalf("%d verdicts reached the gateway for an allowed call, want 1", verdicts)
	}
}

// A connection with no analyzer rule must behave exactly as before: no model
// calls, no verdict packets, no change to the pipeline.
func TestMCPWithoutAnalyzerRuleIsUnchanged(t *testing.T) {
	sessionID := "sid-analyzer-off"
	backend, transport := newTunnelPair(t, sessionID, nil)
	agent := backend.agent
	t.Cleanup(func() { agent.closeMCPProxyConnections(sessionID) })

	connParams := agent.connectionParams(sessionID)
	if connParams.AISessionAnalyzer != nil {
		t.Fatal("fixture unexpectedly carries an analyzer config")
	}
	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionTypeMcpProxy)
	if err != nil {
		t.Fatalf("env: %v", err)
	}

	gw, err := agent.mcpGatewayFor(sessionID, connenv, connParams,
		mcpProxyOpts(connenv, connParams, sessionID, "1"),
		map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)})
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}

	h := gw.Handler()
	_, sid, _ := mcpPost(t, h, "", mcpInitialize)
	mcpPost(t, h, sid, mcpToolsList)
	_, _, body := mcpPost(t, h, sid,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"whoami","arguments":{}}}`)

	if !strings.Contains(body, "ran on pid") {
		t.Fatalf("tool call failed on a connection with no analyzer: %s", body)
	}
	// Close first, so this asserts no verdict was ever produced rather than
	// merely that none had been drained yet.
	agent.closeMCPProxyConnections(sessionID)
	for _, pkt := range transport.packetsOfType(pbclient.MCPProxyConnectionWrite) {
		if len(pkt.Spec[spectypes.AIAnalyzerInfoKey]) > 0 {
			t.Fatal("a connection with no analyzer rule shipped a verdict")
		}
	}
}
