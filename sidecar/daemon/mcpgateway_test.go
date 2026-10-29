package daemon

import (
	"context"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
)

// withMCPGatewayEngine installs an engine builder for one test, as
// withICAEngine does: the registry is process-wide, so the previous builder
// comes back on cleanup. Tests using it must not run in parallel with tests
// that read the registry.
func withMCPGatewayEngine(t *testing.T, build MCPGatewayBuilder) {
	t.Helper()
	mcpGatewayMu.Lock()
	prev := mcpGatewayBuild
	mcpGatewayBuild = build
	mcpGatewayMu.Unlock()
	t.Cleanup(func() {
		mcpGatewayMu.Lock()
		mcpGatewayBuild = prev
		mcpGatewayMu.Unlock()
	})
}

type fakeMCPServer struct {
	lane  MCPGatewayLane
	ended []string
}

func (f *fakeMCPServer) Serve(ctx context.Context) error { <-ctx.Done(); return nil }
func (f *fakeMCPServer) Close() error                    { return nil }
func (f *fakeMCPServer) Addr() net.Addr                  { return nil }
func (f *fakeMCPServer) Stats() (int64, int64, int64)    { return 0, 0, 0 }
func (f *fakeMCPServer) Notes() []string                 { return []string{"engine: fake"} }
func (f *fakeMCPServer) EndSessions(reason string)       { f.ended = append(f.ended, reason) }

func fakeMCPEngine() (MCPGatewayBuilder, *[]*fakeMCPServer) {
	var built []*fakeMCPServer
	return func(lane MCPGatewayLane) (MCPGatewayServer, error) {
		s := &fakeMCPServer{lane: lane}
		built = append(built, s)
		return s, nil
	}, &built
}

// mcpLane is the ADR-0034 example lane, as JSON.
const mcpLane = `{"name":"github","protocol":"mcp","listen":"127.0.0.1:18901","upstream":"https://mcp.example.com/mcp","idle_timeout_sec":120,"mcp_gateway":{"transport":"streamable-http","allowed_tools":["get_*","list_*","search_*"],"denied_tools":["*delete*"],"block_sampling":true,"block_elicitation":true,"on_rug_pull":"kill","max_calls_per_session":200,"max_result_kb":256,"tool_rate_limits":{"search_*":30},"overrides":{"get_file_contents":{"description":"Read one file from a repository."}}}}`

// mcpProblems loads one listener and returns what validation refused, or "".
func mcpProblems(t *testing.T, listener string) string {
	t.Helper()
	if _, err := LoadConfigBytes([]byte(`{"listeners":[` + listener + `]}`)); err != nil {
		return err.Error()
	}
	return ""
}

// The ADR-0034 example lane validates; each carve-out is refused with a
// reason the operator can act on; identity_header is accepted.
func TestMCPGatewayListenerValidation(t *testing.T) {
	if p := mcpProblems(t, mcpLane); p != "" {
		t.Fatalf("the ADR example lane does not validate: %s", p)
	}
	const ok = `"name":"m","protocol":"mcp","listen":"127.0.0.1:1","upstream":"https://h/mcp"`
	cases := []struct{ listener, want string }{
		{`{"name":"m","protocol":"mcp","listen":"0.0.0.0:18901","upstream":"https://h/mcp"}`, "loopback"},
		{`{"name":"m","protocol":"mcp","listen":"127.0.0.1:1","upstream":"h:1"}`, "http(s) URL"},
		{`{"name":"m","protocol":"mcp","network":"unix","listen":"/tmp/m.sock","upstream":"https://h/mcp"}`, "tcp"},
		{`{` + ok + `,"max_conns":4}`, "max_conns"},
		{`{` + ok + `,"mcp_gateway":{"transport":"stdio"}}`, "streamable-http or sse"},
		{`{` + ok + `,"mcp_gateway":{"on_rug_pull":"ignore"}}`, "kill or alert"},
		{`{` + ok + `,"mcp_gateway":{"tool_rate_limits":{"x":0}}}`, "must be positive"},
		{`{` + ok + `,"mcp_gateway":{"approval_tools":["x"]}}`, "unknown field"},
		{`{"name":"m","protocol":"postgres","listen":"127.0.0.1:1","upstream":"h:5432","mcp_gateway":{}}`, "only valid on an mcp listener"},
		{`{` + ok + `,"mask":{"rules":[{"name":"pii","entities":["EMAIL_ADDRESS"],"strategy":"redact"}]}}`, "RD-373"},
		{`{` + ok + `,"analyzer":{"trigger":{"operations":["delete"]}}}`, "RD-372"},
		{`{"name":"m","protocol":"mcp","listen":"127.0.0.1","upstream":"https://h/mcp"}`, "host:port"},
		{`{"name":"m","protocol":"mcp","listen":"127.0.0.1:1","upstream":"http://h/mcp"}`, "loopback only"},
	}
	for _, tc := range cases {
		if p := mcpProblems(t, tc.listener); !strings.Contains(p, tc.want) {
			t.Errorf("%s\n  problems %q lack %q", tc.listener, p, tc.want)
		}
	}
	if p := mcpProblems(t, `{"name":"m","protocol":"mcp","listen":"127.0.0.1:1","upstream":"http://127.0.0.1:18900/mcp"}`); p != "" {
		t.Fatalf("plain http to loopback refused: %s", p)
	}
	if p := mcpProblems(t, `{`+ok+`,"identity_header":"x-user"}`); p != "" {
		t.Fatalf("identity_header refused on an mcp lane: %s", p)
	}
}

// A build without the engine refuses the lane and names the module; with one,
// Validate resolves the lane the engine is built with and binds nothing.
func TestMCPGatewayValidateNeedsAnEngineAndResolvesTheLane(t *testing.T) {
	cfg, err := LoadConfigBytes([]byte(`{"listeners":[` + mcpLane + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(cfg, nil); err == nil || !strings.Contains(err.Error(), "did not link") || !strings.Contains(err.Error(), "sidecar/mcpgateway") {
		t.Fatalf("mcp lane accepted with no engine: %v", err)
	}
	build, built := fakeMCPEngine()
	withMCPGatewayEngine(t, build)
	info, err := Validate(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(info) != 1 || info[0].Protocol != "mcp" || !strings.Contains(strings.Join(info[0].Notes, "\n"), "engine: fake") {
		t.Fatalf("lane info: %+v", info)
	}
	if len(*built) != 1 {
		t.Fatalf("engine built %d times", len(*built))
	}
	lane := (*built)[0].lane
	if lane.Name != "github" || lane.Upstream != "https://mcp.example.com/mcp" || lane.Transport != MCPTransportStreamableHTTP ||
		lane.SessionIdleTimeout != 2*time.Minute || lane.Policy.MaxCallsPerSession != 200 || lane.Policy.ToolRateLimits["search_*"] != 30 ||
		lane.Policy.Overrides["get_file_contents"].Description != "Read one file from a repository." || lane.UpstreamTLS == nil ||
		lane.Open != nil || lane.Log == nil {
		t.Fatalf("resolved lane: %+v", lane)
	}
}

// The header carries protocol:mcp only from a build that links the engine, so
// a plane never serves an mcp lane to a build that refuses it.
func TestTheHeaderCarriesMCPOnlyWithAnEngine(t *testing.T) {
	if slices.Contains(SidecarCapabilities(), "protocol:mcp") {
		t.Fatalf("no engine registered but the header carries protocol:mcp: %v", SidecarCapabilities())
	}
	build, _ := fakeMCPEngine()
	withMCPGatewayEngine(t, build)
	if !slices.Contains(SidecarCapabilities(), "protocol:mcp") {
		t.Fatalf("engine registered but the header lacks protocol:mcp: %v", SidecarCapabilities())
	}
}

func mcpTestLane(t *testing.T, doc string) lane {
	t.Helper()
	cfg, err := LoadConfigBytes([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	lanes, err := buildLanes(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return lanes[0]
}

// One sidecar session per MCP session: session_start, one statement per
// tools/call, allowed or denied with its rule, session_end.
func TestMCPLaneSessionRecordsOneStatementPerCall(t *testing.T) {
	ln := mcpTestLane(t, `{"guardrails":{"rules":[{"name":"no-delete","type":"table","tables":["delete_customer"],"message":"deleting through MCP is not allowed"}]},"listeners":[`+mcpLane+`]}`)
	sink := audit.NewMemorySink(16)
	ctx := context.Background()
	ls, err := openMCPLaneSession(ctx, ln, newLiveRules(ln), sink, true, MCPCaller{Subject: "alice", PeerAddr: "127.0.0.1:5", MCPSessionID: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if v := ls.ToolCall(ctx, "echo", []byte(`{"message":"hi"}`)); !v.Allowed {
		t.Fatalf("echo denied: %+v", v)
	}
	if v := ls.ToolCall(ctx, "delete_customer", []byte(`{"id":"7"}`)); v.Allowed || v.Rule != "no-delete" || !strings.Contains(v.Message, "not allowed") {
		t.Fatalf("delete_customer verdict: %+v", v)
	}
	ls.Record(ctx, "mcp_initialize", map[string]string{"client": "t"})
	ls.Close(ctx)
	rows := sink.Events()
	kinds := make([]string, len(rows))
	for i, r := range rows {
		kinds[i] = string(r.Kind)
		if r.Protocol != inspect.MCP || r.Connection != "github" || r.Principal != "alice" {
			t.Fatalf("row %d is not labelled as this lane: %+v", i, r)
		}
	}
	if want := "session_start,statement,violation,activity,session_end"; strings.Join(kinds, ",") != want {
		t.Fatalf("rows %v, want %s", kinds, want)
	}
	if !rows[1].Allowed || rows[1].Metadata["mcp.tool"] != "echo" || rows[1].Metadata["mcp.session"] != "m1" || !strings.HasPrefix(rows[1].Statement, "echo") {
		t.Fatalf("allowed row: %+v", rows[1])
	}
	if rows[2].Allowed || rows[2].Rule != "no-delete" {
		t.Fatalf("denied row: %+v", rows[2])
	}
}

// A rule swap ends the open MCP sessions (ADR-0034, decision 5).
func TestMCPLaneRuleSwapEndsSessions(t *testing.T) {
	ln := mcpTestLane(t, `{"listeners":[`+mcpLane+`]}`)
	srv := &fakeMCPServer{}
	rules := mcpLaneRules{liveRules: newLiveRules(ln), srv: srv}
	drained := false
	rules.swapLane(ln, func() { drained = true })
	if !drained || len(srv.ended) != 1 || srv.ended[0] != "rules reloaded" {
		t.Fatalf("drained=%v ended=%v", drained, srv.ended)
	}
}

// Mask rules inherited from the top level reach an mcp lane, so one file
// serves every lane and the ADR-0034 example validates; Validate notes that
// they do not apply to this lane until RD-373.
func TestMCPGatewayNotesInheritedMaskRules(t *testing.T) {
	cfg, err := LoadConfigBytes([]byte(`{"mask":{"rules":[{"name":"pii","entities":["EMAIL_ADDRESS"],"strategy":"redact"}]},"listeners":[` + mcpLane + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	build, _ := fakeMCPEngine()
	withMCPGatewayEngine(t, build)
	info, err := Validate(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if notes := strings.Join(info[0].Notes, "\n"); !strings.Contains(notes, "RD-373") {
		t.Fatalf("notes lack the mask note: %s", notes)
	}
}
