package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/identity/token"
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

// mcpLane is the README example lane, as JSON.
const mcpLane = `{"name":"github","protocol":"mcp","listen":"127.0.0.1:18901","upstream":"https://mcp.example.com/mcp","idle_timeout_sec":120,"mcp_gateway":{"transport":"streamable-http","block_sampling":true,"block_elicitation":true,"on_rug_pull":"kill","max_calls_per_session":200,"max_result_kb":256,"tool_rate_limits":{"search_*":30},"overrides":{"get_file_contents":{"description":"Read one file from a repository."}}}}`

// mcpProblems loads one listener and returns what validation refused, or "".
func mcpProblems(t *testing.T, listener string) string {
	t.Helper()
	if _, err := LoadConfigBytes([]byte(`{"listeners":[` + listener + `]}`)); err != nil {
		return err.Error()
	}
	return ""
}

// The README example lane validates; each carve-out is refused with a
// reason the operator can act on; identity_header is accepted.
func TestMCPGatewayListenerValidation(t *testing.T) {
	if p := mcpProblems(t, mcpLane); p != "" {
		t.Fatalf("the README example lane does not validate: %s", p)
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
		{`{` + ok + `,"mcp_gateway":{"denied_tools":["x"]}}`, "unknown field"},
		{`{"name":"m","protocol":"postgres","listen":"127.0.0.1:1","upstream":"h:5432","mcp_gateway":{}}`, "only valid on an mcp listener"},
		{`{` + ok + `,"mask":{"rules":[{"name":"pii","entities":["EMAIL_ADDRESS"],"strategy":"redact"}]}}`, "mask block"},
		{`{` + ok + `,"analyzer":{"trigger":{"operations":["delete"]}}}`, "analyzer does not apply"},
		{`{"name":"m","protocol":"mcp","listen":"127.0.0.1","upstream":"https://h/mcp"}`, "host:port"},
		{`{"name":"m","protocol":"mcp","listen":"127.0.0.1:1","upstream":"http://h/mcp"}`, "loopback only"},
		{`{"name":"m","protocol":"mcp","listen":"127.0.0.1:1","upstream":"https://user:token@h/mcp"}`, "carries no user"},
		{`{"name":"m","protocol":"mcp","listen":"127.0.0.1:1","upstream":"https://h/mcp?api_key=x"}`, "carries no user"},
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
	cfg.lic = licensed(t)
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
	ls, err := openMCPLaneSession(ctx, ln, newLiveRules(ln), sink, true, MCPCaller{Subject: "alice", PeerAddr: "127.0.0.1:5", MCPSessionID: "m1"}, slog.Default())
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

// A rule swap ends the open MCP sessions.
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
// serves every lane and the README example validates; Validate notes that
// they do not apply to this lane yet.
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
	if notes := strings.Join(info[0].Notes, "\n"); !strings.Contains(notes, "mask rules reach this lane") {
		t.Fatalf("notes lack the mask note: %s", notes)
	}
}

// Which rule types judge a tools/call and which judge the catalog.
func TestMCPLaneRuleTypes(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name, rule               string
		callAllowed, listAllowed bool
	}{
		{"table", `{"name":"r","type":"table","tables":["delete_customer"]}`, false, true},
		{"table require_table_match", `{"name":"r","type":"table","tables":["delete_customer"],"require_table_match":true}`, false, true},
		{"deny_words_list in args", `{"name":"r","type":"deny_words_list","words":["prod-db"]}`, false, true},
		{"deny_words_list in catalog", `{"name":"r","type":"deny_words_list","words":["wipes"]}`, true, false},
		{"pattern_match", `{"name":"r","type":"pattern_match","pattern_regex":"\"db\":\\s*\"prod-"}`, false, true},
		{"operation call", `{"name":"r","type":"operation","operations":["call"]}`, false, true},
		{"metadata mcp.tool", `{"name":"r","type":"metadata","metadata":{"mcp.tool":["delete_customer"]}}`, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			ln := mcpTestLane(t, `{"guardrails":{"rules":[`+c.rule+`]},"listeners":[`+mcpLane+`]}`)
			ls, err := openMCPLaneSession(ctx, ln, newLiveRules(ln), audit.NewMemorySink(16), true, MCPCaller{Subject: "alice"}, slog.Default())
			if err != nil {
				t.Fatal(err)
			}
			defer ls.Close(ctx)
			if v := ls.ToolCall(ctx, "delete_customer", []byte(`{"db":"prod-db","id":"7"}`)); v.Allowed != c.callAllowed {
				t.Errorf("tools/call allowed=%v, want %v: %+v", v.Allowed, c.callAllowed, v)
			}
			if v := ls.ToolList(ctx, []MCPToolInfo{{Name: "delete_customer", Description: "Wipes one customer"}}, nil); v.Allowed != c.listAllowed {
				t.Errorf("tools/list allowed=%v, want %v: %+v", v.Allowed, c.listAllowed, v)
			}
		})
	}
}

// OPA is consulted on every tools/call and on the catalog, with the input
// document the README shows; `opa: {responses: false}` keeps the catalog
// from it.
func TestMCPLaneConsultsOPA(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name, opa, wantDirections string
	}{
		{"default", `{"url":%q}`, "client,client,server"},
		{"responses false", `{"url":%q,"responses":false}`, "client,client"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var (
				mu     sync.Mutex
				inputs []map[string]any
			)
			opa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Input map[string]any `json:"input"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decoding OPA input: %v", err)
				}
				mu.Lock()
				inputs = append(inputs, body.Input)
				mu.Unlock()
				allow := fmt.Sprint(body.Input["tables"]) != "[delete_customer]"
				fmt.Fprintf(w, `{"result":{"allow":%v,"message":"denied by opa"}}`, allow)
			}))
			t.Cleanup(opa.Close)

			ln := mcpTestLane(t, fmt.Sprintf(`{"opa":`+c.opa+`,"listeners":[`+mcpLane+`]}`, opa.URL))
			ls, err := openMCPLaneSession(ctx, ln, newLiveRules(ln), audit.NewMemorySink(16), true, MCPCaller{Subject: "alice", MCPSessionID: "m1"}, slog.Default())
			if err != nil {
				t.Fatal(err)
			}
			defer ls.Close(ctx)
			if v := ls.ToolCall(ctx, "get_file_contents", []byte(`{"path":"README.md"}`)); !v.Allowed {
				t.Fatalf("get_file_contents denied: %+v", v)
			}
			if v := ls.ToolCall(ctx, "delete_customer", []byte(`{"id":"7"}`)); v.Allowed || v.Message != "denied by opa" {
				t.Fatalf("delete_customer verdict: %+v", v)
			}
			if v := ls.ToolList(ctx, []MCPToolInfo{{Name: "get_file_contents", Description: "Read a file"}}, nil); !v.Allowed {
				t.Fatalf("tools/list denied: %+v", v)
			}

			mu.Lock()
			defer mu.Unlock()
			dirs := make([]string, len(inputs))
			for i, in := range inputs {
				dirs[i] = fmt.Sprint(in["direction"])
			}
			if got := strings.Join(dirs, ","); got != c.wantDirections {
				t.Fatalf("OPA saw directions %s, want %s", got, c.wantDirections)
			}
			in := inputs[0]
			meta, _ := in["metadata"].(map[string]any)
			sctx, _ := in["context"].(map[string]any)
			if in["protocol"] != "mcp" || in["operation"] != "call" || fmt.Sprint(in["tables"]) != "[get_file_contents]" ||
				in["statement"] != "get_file_contents\n{\"path\":\"README.md\"}" ||
				meta["mcp.tool"] != "get_file_contents" || meta["mcp.method"] != "tools/call" || meta["mcp.session"] != "m1" ||
				sctx["connection"] != "github" {
				t.Fatalf("tools/call input: %v", in)
			}
			if len(inputs) == 3 {
				if in := inputs[2]; in["operation"] != "show" || in["tables"] != nil || in["statement"] != "get_file_contents: Read a file\n" {
					t.Fatalf("tools/list input: %v", in)
				}
			}
		})
	}
}

type activityBrokenSink struct{ audit.Sink }

func (s activityBrokenSink) Write(ctx context.Context, ev audit.Event) error {
	if ev.Kind == audit.KindActivity {
		return errors.New("disk full")
	}
	return s.Sink.Write(ctx, ev)
}

// An audit write the sink refuses is logged, as on the other lanes: the
// session start on a fail-open lane, an activity on a fail-closed one.
func TestMCPLaneLogsAuditWriteFailures(t *testing.T) {
	ln := mcpTestLane(t, `{"listeners":[`+mcpLane+`]}`)
	ctx := context.Background()
	for _, c := range []struct {
		name             string
		sink             audit.Sink
		failOnAuditError bool
		want             string
	}{
		{"start, fail-open", brokenSink{}, false, "mcp session start not recorded"},
		{"activity, fail-closed", activityBrokenSink{audit.NewMemorySink(16)}, true, "mcp activity not recorded"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			ls, err := openMCPLaneSession(ctx, ln, newLiveRules(ln), c.sink, c.failOnAuditError, MCPCaller{Subject: "alice"}, slog.New(slog.NewTextHandler(&buf, nil)))
			if err != nil {
				t.Fatal(err)
			}
			ls.Record(ctx, "mcp_initialize", nil)
			ls.Close(ctx)
			if !strings.Contains(buf.String(), c.want) {
				t.Fatalf("log lacks %q:\n%s", c.want, buf.String())
			}
		})
	}
}

// An enforcing mcp_tool rule hides the tools it denies, and a call to one is
// still denied under the rule's name. An observing lane hides nothing, and
// neither does a rule that defers.
func TestMCPLaneHidesToolsAnMCPToolRuleDenies(t *testing.T) {
	ctx := context.Background()
	rules := `[{"name":"no-delete","type":"mcp_tool","tools":["*delete*"]},{"name":"read-only","type":"mcp_tool","allow":["get_*","*delete*"]}]`
	for _, c := range []struct {
		name, guardrails string
		hidden           []string
		denied           map[string]string
	}{
		{"enforce", `{"rules":` + rules + `}`, []string{"delete_customer", "run_query"}, map[string]string{"delete_customer": "no-delete", "run_query": "read-only"}},
		{"observe", `{"mode":"observe","rules":` + rules + `}`, nil, nil},
		{"defer", `{"rules":[{"name":"ask-opa","type":"mcp_tool","tools":["*delete*"],"action":"defer"}]}`, nil, map[string]string{"delete_customer": "ask-opa"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ln := mcpTestLane(t, `{"guardrails":`+c.guardrails+`,"listeners":[`+mcpLane+`]}`)
			ls, err := openMCPLaneSession(ctx, ln, newLiveRules(ln), audit.NewMemorySink(16), true, MCPCaller{Subject: "alice"}, slog.Default())
			if err != nil {
				t.Fatal(err)
			}
			defer ls.Close(ctx)
			var hidden []string
			for _, tool := range []string{"get_file", "delete_customer", "run_query"} {
				if ls.Hidden(tool) {
					hidden = append(hidden, tool)
				}
				v := ls.ToolCall(ctx, tool, []byte(`{}`))
				want, deny := c.denied[tool]
				if v.Allowed == deny || v.Rule != want {
					t.Errorf("%s: %+v, want denied=%v by %q", tool, v, deny, want)
				}
			}
			if !slices.Equal(hidden, c.hidden) {
				t.Errorf("hidden %v, want %v", hidden, c.hidden)
			}
		})
	}
}

// The catalog statement names the tools the lane left out of tools/list.
func TestMCPLaneRecordsHiddenTools(t *testing.T) {
	ctx := context.Background()
	ln := mcpTestLane(t, `{"listeners":[`+mcpLane+`]}`)
	sink := audit.NewMemorySink(16)
	ls, err := openMCPLaneSession(ctx, ln, newLiveRules(ln), sink, true, MCPCaller{Subject: "alice"}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	ls.ToolList(ctx, []MCPToolInfo{{Name: "get_file", Description: "Read a file"}}, []string{"delete_customer", "run_query"})
	ls.Close(ctx)
	for _, r := range sink.Events() {
		if r.Metadata[inspect.MetadataMCPMethod] == "tools/list" {
			if got := r.Metadata[inspect.MetadataMCPToolsHidden]; got != "delete_customer,run_query" || strings.Contains(r.Statement, "delete_customer") {
				t.Fatalf("catalog row: hidden %q, statement %q", got, r.Statement)
			}
			return
		}
	}
	t.Fatal("no catalog row")
}

// A rule reload swaps the catalog filter with the rules: a session opened
// after it hides by the new rules.
func TestMCPLaneHideSwapsWithTheRules(t *testing.T) {
	ctx := context.Background()
	before := mcpTestLane(t, `{"listeners":[`+mcpLane+`]}`)
	after := mcpTestLane(t, `{"guardrails":{"rules":[{"name":"no-echo","type":"mcp_tool","tools":["echo"]}]},"listeners":[`+mcpLane+`]}`)
	live := newLiveRules(before)
	hidesEcho := func() bool {
		ls, err := openMCPLaneSession(ctx, before, live, audit.NewMemorySink(16), true, MCPCaller{Subject: "alice"}, slog.Default())
		if err != nil {
			t.Fatal(err)
		}
		defer ls.Close(ctx)
		return ls.Hidden("echo")
	}
	if hidesEcho() {
		t.Fatal("echo hidden before the reload")
	}
	live.swapLane(after, func() {})
	if !hidesEcho() {
		t.Fatal("echo not hidden after the reload")
	}
}

// caller_token lets an mcp lane bind off loopback; its header cannot be one
// MCP reads, and downstream_tls is accepted.
func TestMCPCallerTokenValidation(t *testing.T) {
	const base = `"name":"m","protocol":"mcp","upstream":"https://h/mcp"`
	const tok = `"caller_token":{"file":"/etc/tokens","name":"agents"}`
	cert, key := tlsKeypairFiles(t)
	for _, listener := range []string{
		`{` + base + `,"listen":"0.0.0.0:18901",` + tok + `}`,
		fmt.Sprintf(`{`+base+`,"listen":"0.0.0.0:18901",`+tok+`,"downstream_tls":{"cert_file":%q,"key_file":%q}}`, cert, key),
		fmt.Sprintf(`{`+base+`,"listen":"127.0.0.1:1","downstream_tls":{"cert_file":%q,"key_file":%q}}`, cert, key),
	} {
		if p := mcpProblems(t, listener); p != "" {
			t.Errorf("%s\n  refused: %s", listener, p)
		}
	}
	for _, tc := range []struct{ listener, want string }{
		{`{` + base + `,"listen":"0.0.0.0:18901"}`, "only with caller_token"},
		{`{` + base + `,"listen":"127.0.0.1:1","caller_token":{"header":"Mcp-Session-Id","file":"/etc/tokens","name":"agents"}}`, "a header MCP uses"},
		{`{` + base + `,"listen":"127.0.0.1:1","caller_token":{"header":"mcp-protocol-version","file":"/etc/tokens","name":"agents"}}`, "a header MCP uses"},
		{`{` + base + `,"listen":"127.0.0.1:1","caller_token":{"header":"Last-Event-ID","file":"/etc/tokens","name":"agents"}}`, "a header MCP uses"},
		{`{` + base + `,"listen":"127.0.0.1:1","identity_header":"x-user",` + tok + `}`, "both name the caller"},
	} {
		if p := mcpProblems(t, tc.listener); !strings.Contains(p, tc.want) {
			t.Errorf("%s\n  problems %q lack %q", tc.listener, p, tc.want)
		}
	}
}

// The engine's caller check reads the lane's token file: a listed token names
// the configured caller under a key per token that holds no token, and a
// rotation applies without a rebuild.
func TestAnMCPLaneCallerCheckReadsItsTokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens")
	if err := os.WriteFile(path, []byte(goodToken+"\n"+newToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cert, key := tlsKeypairFiles(t)
	ln := mcpTestLane(t, fmt.Sprintf(`{"listeners":[{"name":"m","protocol":"mcp","listen":"0.0.0.0:18901","upstream":"https://h/mcp",`+
		`"caller_token":{"file":%q,"name":"claude-agents"},"downstream_tls":{"cert_file":%q,"key_file":%q}}]}`, path, cert, key))
	build, built := fakeMCPEngine()
	withMCPGatewayEngine(t, build)
	discard := slog.New(slog.DiscardHandler)
	if _, _, err := buildMCPGatewayServer(ln, AuditConfig{}, nil, discard); err == nil || !strings.Contains(err.Error(), "not opened") {
		t.Fatalf("a caller_token lane built without its token file: %v", err)
	}
	lanes := []lane{ln}
	files, err := openCallerTokens(lanes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := buildMCPGatewayServer(lanes[0], AuditConfig{}, nil, discard); err != nil {
		t.Fatal(err)
	}
	resolved := (*built)[0].lane
	if resolved.DownstreamTLS == nil || resolved.Caller == nil || resolved.Caller.Header != "x-hoop-token" {
		t.Fatalf("resolved lane: %+v", resolved)
	}
	resolve := func(credential string) (string, string, error) {
		return resolved.Caller.Resolve(context.Background(), credential)
	}
	subject, k1, err := resolve(" " + goodToken + " ")
	if err != nil || subject != "claude-agents" || k1 == "" || strings.Contains(k1, goodToken) {
		t.Fatalf("listed token: subject %q key %q err %v", subject, k1, err)
	}
	if _, again, _ := resolve(goodToken); again != k1 {
		t.Fatal("one token resolved to two keys")
	}
	if _, k2, _ := resolve(newToken); k2 == k1 {
		t.Fatal("two tokens share a key, so one could use the other's session")
	}
	for credential, want := range map[string]error{"": token.ErrNoToken, "wrong-token": token.ErrInvalidToken, goodToken + ", " + goodToken: token.ErrInvalidToken} {
		if _, _, err := resolve(credential); !errors.Is(err, want) {
			t.Errorf("credential %q: %v, want %v", credential, err, want)
		}
	}
	if err := os.WriteFile(path, []byte(newToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	files[0].Reload()
	if _, _, err := resolve(goodToken); !errors.Is(err, token.ErrInvalidToken) {
		t.Fatalf("a removed token still admitted: %v", err)
	}
	if _, _, err := resolve(newToken); err != nil {
		t.Fatalf("the remaining token refused: %v", err)
	}
}

// 1.289.0 decodes caller_token and downstream_tls but refuses them on an mcp
// lane, so the plane serves them there only to a build that reads them.
func TestAnMCPCallerTokenLaneNeedsTheCapability(t *testing.T) {
	old := Handshake{Version: "1.289.0", Capabilities: []string{"protocol:mcp", "caller_token", "address_url", "rule:operation"}}
	current := Handshake{Capabilities: append(slices.Clone(old.Capabilities), CapabilityMCPCallerToken)}
	for name, l := range map[string]ListenerConfig{
		"caller_token":   {CallerToken: &CallerTokenConfig{File: "/etc/tokens", Name: "agents"}},
		"downstream_tls": {DownstreamTLS: &TLSConfig{CertFile: "/etc/tls.crt", KeyFile: "/etc/tls.key"}},
	} {
		l.Name, l.Protocol, l.Listen, l.Upstream = "tools", "mcp", "0.0.0.0:18901", "https://h/mcp"
		cfg := Config{Listeners: []ListenerConfig{l}}
		if err := CheckServable(cfg, old); err == nil || !strings.Contains(err.Error(), CapabilityMCPCallerToken) {
			t.Errorf("%s: an older build was served it on an mcp lane: %v", name, err)
		}
		if err := CheckServable(cfg, current); err != nil {
			t.Errorf("%s: a build that reads it was refused: %v", name, err)
		}
	}
	if !slices.Contains(SidecarCapabilities(), CapabilityMCPCallerToken) {
		t.Fatalf("the header lacks %s: %v", CapabilityMCPCallerToken, SidecarCapabilities())
	}
}
